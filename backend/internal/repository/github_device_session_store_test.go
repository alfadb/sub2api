//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGitHubDeviceSessionStore_Set_RedisError(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	store := NewGitHubDeviceSessionStore(rdb)
	err := store.Set(context.Background(), "broken", &service.GitHubDeviceSession{
		AccountID:          1,
		AccountConcurrency: 1,
		ProxyURL:           "",
		ClientID:           "cid",
		Scope:              "scope",
		DeviceCode:         "dc",
		ExpiresAtUnix:      time.Now().Add(time.Minute).Unix(),
		IntervalSeconds:    5,
		CreatedAtUnix:      time.Now().Unix(),
	}, time.Minute)
	require.Error(t, err)
}
