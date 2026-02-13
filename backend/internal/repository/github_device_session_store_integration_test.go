//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GitHubDeviceSessionStoreSuite struct {
	IntegrationRedisSuite
	store service.GitHubDeviceSessionStore
}

func (s *GitHubDeviceSessionStoreSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.store = NewGitHubDeviceSessionStore(s.rdb)
}

func (s *GitHubDeviceSessionStoreSuite) TestSetGetDelete() {
	id := "session-1"
	createdAt := time.Now().Unix()
	expiresAt := time.Now().Add(2 * time.Minute).Unix()

	sess := &service.GitHubDeviceSession{
		AccountID:          123,
		AccountConcurrency: 7,
		ProxyURL:           "http://proxy.local",
		ClientID:           "client-1",
		Scope:              "read:user",
		DeviceCode:         "device-code",
		ExpiresAtUnix:      expiresAt,
		IntervalSeconds:    5,
		CreatedAtUnix:      createdAt,
	}

	require.NoError(s.T(), s.store.Set(s.ctx, id, sess, 2*time.Minute))

	got, ok, err := s.store.Get(s.ctx, id)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	require.Equal(s.T(), sess, got)

	key := gitHubDeviceSessionKeyPrefix + id
	ttl, err := s.rdb.TTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(ttl, time.Minute, 2*time.Minute)

	require.NoError(s.T(), s.store.Delete(s.ctx, id))
	_, ok, err = s.store.Get(s.ctx, id)
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
}

func (s *GitHubDeviceSessionStoreSuite) TestGetMissing() {
	_, ok, err := s.store.Get(s.ctx, "missing")
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
}

func (s *GitHubDeviceSessionStoreSuite) TestSetWithNonPositiveTTLDeletes() {
	id := "session-ttl-0"
	sess := &service.GitHubDeviceSession{AccountID: 1, ExpiresAtUnix: time.Now().Add(time.Minute).Unix()}
	require.NoError(s.T(), s.store.Set(s.ctx, id, sess, time.Minute))
	require.NoError(s.T(), s.store.Set(s.ctx, id, sess, 0))
	_, ok, err := s.store.Get(s.ctx, id)
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
}

func TestGitHubDeviceSessionStoreSuite(t *testing.T) {
	suite.Run(t, new(GitHubDeviceSessionStoreSuite))
}
