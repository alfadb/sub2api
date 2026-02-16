//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type billingCacheStubForEligibility struct {
	balance float64
	sub     *SubscriptionCacheData
}

func (s *billingCacheStubForEligibility) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	return s.balance, nil
}

func (s *billingCacheStubForEligibility) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	return nil
}

func (s *billingCacheStubForEligibility) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	return nil
}

func (s *billingCacheStubForEligibility) InvalidateUserBalance(ctx context.Context, userID int64) error {
	return nil
}

func (s *billingCacheStubForEligibility) GetSubscriptionCache(ctx context.Context, userID, groupID int64) (*SubscriptionCacheData, error) {
	return s.sub, nil
}

func (s *billingCacheStubForEligibility) SetSubscriptionCache(ctx context.Context, userID, groupID int64, data *SubscriptionCacheData) error {
	return nil
}

func (s *billingCacheStubForEligibility) UpdateSubscriptionUsage(ctx context.Context, userID, groupID int64, cost float64) error {
	return nil
}

func (s *billingCacheStubForEligibility) InvalidateSubscriptionCache(ctx context.Context, userID, groupID int64) error {
	return nil
}

func TestBillingCacheService_CheckBillingEligibility_SubscriptionGroupWithoutSubscriptionObject(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cache := &billingCacheStubForEligibility{
		balance: 0,
		sub: &SubscriptionCacheData{
			Status:    SubscriptionStatusActive,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		},
	}
	svc := NewBillingCacheService(cache, nil, nil, cfg)
	t.Cleanup(svc.Stop)

	user := &User{ID: 1}
	group := &Group{ID: 2, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive}

	err := svc.CheckBillingEligibility(context.Background(), user, &APIKey{}, group, nil)
	require.NoError(t, err)
}
