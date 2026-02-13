package service

import (
	"context"
	"sync"
	"time"
)

type GitHubDeviceSession struct {
	AccountID          int64  `json:"account_id"`
	AccountConcurrency int    `json:"account_concurrency"`
	ProxyURL           string `json:"proxy_url"`
	ClientID           string `json:"client_id"`
	Scope              string `json:"scope"`
	DeviceCode         string `json:"device_code"`

	ExpiresAtUnix   int64 `json:"expires_at_unix"`
	IntervalSeconds int64 `json:"interval_seconds"`
	CreatedAtUnix   int64 `json:"created_at_unix"`
}

type GitHubDeviceSessionStore interface {
	Get(ctx context.Context, id string) (*GitHubDeviceSession, bool, error)
	Set(ctx context.Context, id string, sess *GitHubDeviceSession, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
}

type inMemoryGitHubDeviceSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*GitHubDeviceSession
}

func NewInMemoryGitHubDeviceSessionStore() GitHubDeviceSessionStore {
	return &inMemoryGitHubDeviceSessionStore{sessions: make(map[string]*GitHubDeviceSession)}
}

func (s *inMemoryGitHubDeviceSessionStore) Get(_ context.Context, id string) (*GitHubDeviceSession, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok || sess == nil {
		return nil, false, nil
	}
	return sess, true, nil
}

func (s *inMemoryGitHubDeviceSessionStore) Set(_ context.Context, id string, sess *GitHubDeviceSession, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = sess
	return nil
}

func (s *inMemoryGitHubDeviceSessionStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}
