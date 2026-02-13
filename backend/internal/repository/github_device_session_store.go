package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const gitHubDeviceSessionKeyPrefix = "github:device_session:"

type gitHubDeviceSessionStore struct {
	rdb *redis.Client
}

func NewGitHubDeviceSessionStore(rdb *redis.Client) service.GitHubDeviceSessionStore {
	return &gitHubDeviceSessionStore{rdb: rdb}
}

func (s *gitHubDeviceSessionStore) Get(ctx context.Context, id string) (*service.GitHubDeviceSession, bool, error) {
	key := fmt.Sprintf("%s%s", gitHubDeviceSessionKeyPrefix, id)
	b, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var sess service.GitHubDeviceSession
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, false, err
	}
	return &sess, true, nil
}

func (s *gitHubDeviceSessionStore) Set(ctx context.Context, id string, sess *service.GitHubDeviceSession, ttl time.Duration) error {
	key := fmt.Sprintf("%s%s", gitHubDeviceSessionKeyPrefix, id)
	if ttl <= 0 {
		return s.rdb.Del(ctx, key).Err()
	}
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, b, ttl).Err()
}

func (s *gitHubDeviceSessionStore) Delete(ctx context.Context, id string) error {
	key := fmt.Sprintf("%s%s", gitHubDeviceSessionKeyPrefix, id)
	return s.rdb.Del(ctx, key).Err()
}
