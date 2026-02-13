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
	mu       sync.Mutex
	sessions map[string]inMemoryGitHubDeviceSession
}

type inMemoryGitHubDeviceSession struct {
	sess      *GitHubDeviceSession
	expiresAt time.Time
}

func NewInMemoryGitHubDeviceSessionStore() GitHubDeviceSessionStore {
	return &inMemoryGitHubDeviceSessionStore{sessions: make(map[string]inMemoryGitHubDeviceSession)}
}

func (s *inMemoryGitHubDeviceSessionStore) Get(_ context.Context, id string) (*GitHubDeviceSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[id]
	if !ok || entry.sess == nil {
		return nil, false, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(s.sessions, id)
		return nil, false, nil
	}
	return entry.sess, true, nil
}

func (s *inMemoryGitHubDeviceSessionStore) Set(_ context.Context, id string, sess *GitHubDeviceSession, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ttl <= 0 || sess == nil {
		delete(s.sessions, id)
		return nil
	}
	s.sessions[id] = inMemoryGitHubDeviceSession{sess: sess, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (s *inMemoryGitHubDeviceSessionStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}
