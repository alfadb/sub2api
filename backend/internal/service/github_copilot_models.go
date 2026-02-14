package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

func (s *GatewayService) ListGitHubCopilotModels(ctx context.Context, groupID *int64) ([]openai.Model, error) {
	if s == nil {
		return nil, errors.New("gateway service is nil")
	}
	if s.githubCopilotToken == nil {
		return nil, errors.New("github copilot token provider not configured")
	}
	accounts, _, err := s.listSchedulableAccounts(ctx, groupID, PlatformCopilot, false)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("no available copilot accounts")
	}
	acc := &accounts[0]
	return s.githubCopilotToken.ListModels(ctx, acc)
}
