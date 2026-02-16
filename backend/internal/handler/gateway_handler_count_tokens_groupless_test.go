//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	mw "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	svc "github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type billingCacheStubForCountTokens struct {
	balance float64
	sub     *svc.SubscriptionCacheData
}

func (s *billingCacheStubForCountTokens) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	return s.balance, nil
}

func (s *billingCacheStubForCountTokens) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	return nil
}

func (s *billingCacheStubForCountTokens) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	return nil
}

func (s *billingCacheStubForCountTokens) InvalidateUserBalance(ctx context.Context, userID int64) error {
	return nil
}

func (s *billingCacheStubForCountTokens) GetSubscriptionCache(ctx context.Context, userID, groupID int64) (*svc.SubscriptionCacheData, error) {
	return s.sub, nil
}

func (s *billingCacheStubForCountTokens) SetSubscriptionCache(ctx context.Context, userID, groupID int64, data *svc.SubscriptionCacheData) error {
	return nil
}

func (s *billingCacheStubForCountTokens) UpdateSubscriptionUsage(ctx context.Context, userID, groupID int64, cost float64) error {
	return nil
}

func (s *billingCacheStubForCountTokens) InvalidateSubscriptionCache(ctx context.Context, userID, groupID int64) error {
	return nil
}

type accountRepoStubForCountTokens struct {
	listByGroupAccounts []svc.Account
}

func (s *accountRepoStubForCountTokens) Create(ctx context.Context, account *svc.Account) error {
	panic("unexpected Create call")
}

func (s *accountRepoStubForCountTokens) GetByID(ctx context.Context, id int64) (*svc.Account, error) {
	panic("unexpected GetByID call")
}

func (s *accountRepoStubForCountTokens) GetByIDs(ctx context.Context, ids []int64) ([]*svc.Account, error) {
	panic("unexpected GetByIDs call")
}

func (s *accountRepoStubForCountTokens) ExistsByID(ctx context.Context, id int64) (bool, error) {
	panic("unexpected ExistsByID call")
}

func (s *accountRepoStubForCountTokens) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*svc.Account, error) {
	panic("unexpected GetByCRSAccountID call")
}

func (s *accountRepoStubForCountTokens) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	panic("unexpected ListCRSAccountIDs call")
}

func (s *accountRepoStubForCountTokens) Update(ctx context.Context, account *svc.Account) error {
	panic("unexpected Update call")
}

func (s *accountRepoStubForCountTokens) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *accountRepoStubForCountTokens) List(ctx context.Context, params pagination.PaginationParams) ([]svc.Account, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *accountRepoStubForCountTokens) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64) ([]svc.Account, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *accountRepoStubForCountTokens) ListByGroup(ctx context.Context, groupID int64) ([]svc.Account, error) {
	panic("unexpected ListByGroup call")
}

func (s *accountRepoStubForCountTokens) ListActive(ctx context.Context) ([]svc.Account, error) {
	panic("unexpected ListActive call")
}

func (s *accountRepoStubForCountTokens) ListByPlatform(ctx context.Context, platform string) ([]svc.Account, error) {
	panic("unexpected ListByPlatform call")
}

func (s *accountRepoStubForCountTokens) UpdateLastUsed(ctx context.Context, id int64) error {
	panic("unexpected UpdateLastUsed call")
}

func (s *accountRepoStubForCountTokens) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	panic("unexpected BatchUpdateLastUsed call")
}

func (s *accountRepoStubForCountTokens) SetError(ctx context.Context, id int64, errorMsg string) error {
	panic("unexpected SetError call")
}

func (s *accountRepoStubForCountTokens) ClearError(ctx context.Context, id int64) error {
	panic("unexpected ClearError call")
}

func (s *accountRepoStubForCountTokens) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	panic("unexpected SetSchedulable call")
}

func (s *accountRepoStubForCountTokens) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	panic("unexpected AutoPauseExpiredAccounts call")
}

func (s *accountRepoStubForCountTokens) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	panic("unexpected BindGroups call")
}

func (s *accountRepoStubForCountTokens) ListSchedulable(ctx context.Context) ([]svc.Account, error) {
	panic("unexpected ListSchedulable call")
}

func (s *accountRepoStubForCountTokens) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]svc.Account, error) {
	return append([]svc.Account(nil), s.listByGroupAccounts...), nil
}

func (s *accountRepoStubForCountTokens) ListSchedulableByGroupIDs(ctx context.Context, groupIDs []int64) ([]svc.Account, error) {
	panic("unexpected ListSchedulableByGroupIDs call")
}

func (s *accountRepoStubForCountTokens) ListSchedulableByPlatform(ctx context.Context, platform string) ([]svc.Account, error) {
	panic("unexpected ListSchedulableByPlatform call")
}

func (s *accountRepoStubForCountTokens) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]svc.Account, error) {
	return nil, errors.New("boom")
}

func (s *accountRepoStubForCountTokens) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]svc.Account, error) {
	panic("unexpected ListSchedulableByPlatforms call")
}

func (s *accountRepoStubForCountTokens) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]svc.Account, error) {
	panic("unexpected ListSchedulableByGroupIDAndPlatforms call")
}

func (s *accountRepoStubForCountTokens) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	panic("unexpected SetRateLimited call")
}

func (s *accountRepoStubForCountTokens) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time) error {
	panic("unexpected SetModelRateLimit call")
}

func (s *accountRepoStubForCountTokens) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	panic("unexpected SetOverloaded call")
}

func (s *accountRepoStubForCountTokens) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	panic("unexpected SetTempUnschedulable call")
}

func (s *accountRepoStubForCountTokens) ClearTempUnschedulable(ctx context.Context, id int64) error {
	panic("unexpected ClearTempUnschedulable call")
}

func (s *accountRepoStubForCountTokens) ClearRateLimit(ctx context.Context, id int64) error {
	panic("unexpected ClearRateLimit call")
}

func (s *accountRepoStubForCountTokens) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	panic("unexpected ClearAntigravityQuotaScopes call")
}

func (s *accountRepoStubForCountTokens) ClearModelRateLimits(ctx context.Context, id int64) error {
	panic("unexpected ClearModelRateLimits call")
}

func (s *accountRepoStubForCountTokens) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	panic("unexpected UpdateSessionWindow call")
}

func (s *accountRepoStubForCountTokens) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	panic("unexpected UpdateExtra call")
}

func (s *accountRepoStubForCountTokens) BulkUpdate(ctx context.Context, ids []int64, updates svc.AccountBulkUpdate) (int64, error) {
	panic("unexpected BulkUpdate call")
}

type groupRepoStubForCountTokens struct {
	group *svc.Group
}

func (s *groupRepoStubForCountTokens) Create(ctx context.Context, group *svc.Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStubForCountTokens) GetByID(ctx context.Context, id int64) (*svc.Group, error) {
	panic("unexpected GetByID call")
}

func (s *groupRepoStubForCountTokens) GetByIDLite(ctx context.Context, id int64) (*svc.Group, error) {
	if s.group != nil && s.group.ID == id {
		return s.group, nil
	}
	return nil, svc.ErrGroupNotFound
}

func (s *groupRepoStubForCountTokens) Update(ctx context.Context, group *svc.Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStubForCountTokens) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForCountTokens) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForCountTokens) List(ctx context.Context, params pagination.PaginationParams) ([]svc.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForCountTokens) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]svc.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStubForCountTokens) ListActive(ctx context.Context) ([]svc.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForCountTokens) ListActiveByPlatform(ctx context.Context, platform string) ([]svc.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForCountTokens) ListPublicGroupIDs(ctx context.Context) ([]int64, error) {
	return nil, nil
}

func (s *groupRepoStubForCountTokens) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForCountTokens) GetAccountCount(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForCountTokens) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForCountTokens) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForCountTokens) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForCountTokens) UpdateSortOrders(ctx context.Context, updates []svc.GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

func TestGatewayHandler_CountTokens_GroupLessKeyResolvesGroupBeforeBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(7)
	user := &svc.User{ID: 1, AllowedGroups: []int64{groupID}}
	apiKey := &svc.APIKey{ID: 10, User: user}

	cache := &billingCacheStubForCountTokens{
		balance: 0,
		sub: &svc.SubscriptionCacheData{
			Status:    svc.SubscriptionStatusActive,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		},
	}
	billingSvc := svc.NewBillingCacheService(cache, nil, nil, &config.Config{RunMode: config.RunModeStandard})
	t.Cleanup(billingSvc.Stop)

	group := &svc.Group{ID: groupID, Platform: svc.PlatformOpenAI, SubscriptionType: svc.SubscriptionTypeSubscription, Status: svc.StatusActive}
	groupRepo := &groupRepoStubForCountTokens{group: group}
	accountRepo := &accountRepoStubForCountTokens{listByGroupAccounts: []svc.Account{{ID: 1, Platform: svc.PlatformOpenAI, Status: svc.StatusActive, Schedulable: true}}}
	gatewaySvc := svc.NewGatewayService(
		accountRepo,
		groupRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		&config.Config{RunMode: config.RunModeStandard},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h := &GatewayHandler{
		gatewayService:      gatewaySvc,
		billingCacheService: billingSvc,
	}

	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	c.Set(string(mw.ContextKeyAPIKey), apiKey)
	c.Set(string(mw.ContextKeyUser), mw.AuthSubject{UserID: user.ID, Concurrency: 1})

	h.CountTokens(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
