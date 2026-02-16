//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type usageLogRepoStubForRecordUsage struct {
	created []*UsageLog
}

func (s *usageLogRepoStubForRecordUsage) Create(ctx context.Context, log *UsageLog) (inserted bool, err error) {
	s.created = append(s.created, log)
	return true, nil
}

func (s *usageLogRepoStubForRecordUsage) GetByID(ctx context.Context, id int64) (*UsageLog, error) {
	panic("unexpected GetByID call")
}

func (s *usageLogRepoStubForRecordUsage) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *usageLogRepoStubForRecordUsage) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByUser call")
}

func (s *usageLogRepoStubForRecordUsage) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByAPIKey call")
}

func (s *usageLogRepoStubForRecordUsage) ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByAccount call")
}

func (s *usageLogRepoStubForRecordUsage) ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserAndTimeRange call")
}

func (s *usageLogRepoStubForRecordUsage) ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByAPIKeyAndTimeRange call")
}

func (s *usageLogRepoStubForRecordUsage) ListByAccountAndTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByAccountAndTimeRange call")
}

func (s *usageLogRepoStubForRecordUsage) ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListByModelAndTimeRange call")
}

func (s *usageLogRepoStubForRecordUsage) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	panic("unexpected GetAccountWindowStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetAccountTodayStats(ctx context.Context, accountID int64) (*usagestats.AccountStats, error) {
	panic("unexpected GetAccountTodayStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetDashboardStats(ctx context.Context) (*usagestats.DashboardStats, error) {
	panic("unexpected GetDashboardStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, stream *bool, billingType *int8) ([]usagestats.TrendDataPoint, error) {
	panic("unexpected GetUsageTrendWithFilters call")
}

func (s *usageLogRepoStubForRecordUsage) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, stream *bool, billingType *int8) ([]usagestats.ModelStat, error) {
	panic("unexpected GetModelStatsWithFilters call")
}

func (s *usageLogRepoStubForRecordUsage) GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.APIKeyUsageTrendPoint, error) {
	panic("unexpected GetAPIKeyUsageTrend call")
}

func (s *usageLogRepoStubForRecordUsage) GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.UserUsageTrendPoint, error) {
	panic("unexpected GetUserUsageTrend call")
}

func (s *usageLogRepoStubForRecordUsage) GetBatchUserUsageStats(ctx context.Context, userIDs []int64) (map[int64]*usagestats.BatchUserUsageStats, error) {
	panic("unexpected GetBatchUserUsageStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetBatchAPIKeyUsageStats(ctx context.Context, apiKeyIDs []int64) (map[int64]*usagestats.BatchAPIKeyUsageStats, error) {
	panic("unexpected GetBatchAPIKeyUsageStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetUserDashboardStats(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	panic("unexpected GetUserDashboardStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*usagestats.UserDashboardStats, error) {
	panic("unexpected GetAPIKeyDashboardStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) ([]usagestats.TrendDataPoint, error) {
	panic("unexpected GetUserUsageTrendByUserID call")
}

func (s *usageLogRepoStubForRecordUsage) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagestats.ModelStat, error) {
	panic("unexpected GetUserModelStats call")
}

func (s *usageLogRepoStubForRecordUsage) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *usageLogRepoStubForRecordUsage) GetGlobalStats(ctx context.Context, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	panic("unexpected GetGlobalStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetStatsWithFilters(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	panic("unexpected GetStatsWithFilters call")
}

func (s *usageLogRepoStubForRecordUsage) GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	panic("unexpected GetAccountUsageStats call")
}

func (s *usageLogRepoStubForRecordUsage) GetUserStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	panic("unexpected GetUserStatsAggregated call")
}

func (s *usageLogRepoStubForRecordUsage) GetAPIKeyStatsAggregated(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	panic("unexpected GetAPIKeyStatsAggregated call")
}

func (s *usageLogRepoStubForRecordUsage) GetAccountStatsAggregated(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	panic("unexpected GetAccountStatsAggregated call")
}

func (s *usageLogRepoStubForRecordUsage) GetModelStatsAggregated(ctx context.Context, modelName string, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	panic("unexpected GetModelStatsAggregated call")
}

func (s *usageLogRepoStubForRecordUsage) GetDailyStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) ([]map[string]any, error) {
	panic("unexpected GetDailyStatsAggregated call")
}

type userRepoStubForRecordUsage struct {
	userRepoStub
	deductCalls int
}

func (s *userRepoStubForRecordUsage) DeductBalance(ctx context.Context, id int64, amount float64) error {
	s.deductCalls++
	return nil
}

type userSubRepoStubForRecordUsage struct {
	active          *UserSubscription
	getActiveCalls  int
	incrementCalls  int
	incrementSubIDs []int64
}

func (s *userSubRepoStubForRecordUsage) Create(ctx context.Context, sub *UserSubscription) error {
	panic("unexpected Create call")
}

func (s *userSubRepoStubForRecordUsage) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	panic("unexpected GetByID call")
}

func (s *userSubRepoStubForRecordUsage) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	panic("unexpected GetByUserIDAndGroupID call")
}

func (s *userSubRepoStubForRecordUsage) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	s.getActiveCalls++
	if s.active == nil {
		return nil, errors.New("subscription not found")
	}
	return s.active, nil
}

func (s *userSubRepoStubForRecordUsage) Update(ctx context.Context, sub *UserSubscription) error {
	panic("unexpected Update call")
}

func (s *userSubRepoStubForRecordUsage) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *userSubRepoStubForRecordUsage) ListByUserID(ctx context.Context, userID int64) ([]UserSubscription, error) {
	panic("unexpected ListByUserID call")
}

func (s *userSubRepoStubForRecordUsage) ListActiveByUserID(ctx context.Context, userID int64) ([]UserSubscription, error) {
	panic("unexpected ListActiveByUserID call")
}

func (s *userSubRepoStubForRecordUsage) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

func (s *userSubRepoStubForRecordUsage) List(ctx context.Context, params pagination.PaginationParams, userID, groupID *int64, status, sortBy, sortOrder string) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *userSubRepoStubForRecordUsage) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	panic("unexpected ExistsByUserIDAndGroupID call")
}

func (s *userSubRepoStubForRecordUsage) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	panic("unexpected ExtendExpiry call")
}

func (s *userSubRepoStubForRecordUsage) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	panic("unexpected UpdateStatus call")
}

func (s *userSubRepoStubForRecordUsage) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	panic("unexpected UpdateNotes call")
}

func (s *userSubRepoStubForRecordUsage) ActivateWindows(ctx context.Context, id int64, start time.Time) error {
	panic("unexpected ActivateWindows call")
}

func (s *userSubRepoStubForRecordUsage) ResetDailyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	panic("unexpected ResetDailyUsage call")
}

func (s *userSubRepoStubForRecordUsage) ResetWeeklyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	panic("unexpected ResetWeeklyUsage call")
}

func (s *userSubRepoStubForRecordUsage) ResetMonthlyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	panic("unexpected ResetMonthlyUsage call")
}

func (s *userSubRepoStubForRecordUsage) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	s.incrementCalls++
	s.incrementSubIDs = append(s.incrementSubIDs, id)
	return nil
}

func (s *userSubRepoStubForRecordUsage) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	panic("unexpected BatchUpdateExpiredStatus call")
}

func TestGatewayService_RecordUsage_SubscriptionGroupWithoutSubscriptionObject(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingService := NewBillingService(cfg, nil)

	usageRepo := &usageLogRepoStubForRecordUsage{}
	userRepo := &userRepoStubForRecordUsage{}
	subRepo := &userSubRepoStubForRecordUsage{active: &UserSubscription{ID: 99}}

	svc := &GatewayService{
		usageLogRepo:        usageRepo,
		userRepo:            userRepo,
		userSubRepo:         subRepo,
		cfg:                 cfg,
		billingService:      billingService,
		billingCacheService: &BillingCacheService{cfg: cfg},
		deferredService:     &DeferredService{},
	}

	user := &User{ID: 1}
	group := &Group{ID: 2, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive}
	groupID := group.ID
	apiKey := &APIKey{ID: 3, User: user, Group: group, GroupID: &groupID}
	account := &Account{ID: 4, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	result := &ForwardResult{
		RequestID: "req_1",
		Model:     "claude-3-5-haiku",
		Usage: ClaudeUsage{
			InputTokens:  100,
			OutputTokens: 0,
		},
		Duration: 1 * time.Second,
	}

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result:       result,
		APIKey:       apiKey,
		User:         user,
		Account:      account,
		Subscription: nil,
	})
	require.NoError(t, err)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 1, subRepo.incrementCalls)
}
