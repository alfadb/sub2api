package service

import (
	"context"
	"errors"
)

// CopilotTokenProvider 管理 GitHub Copilot 账户的 access_token。
// GitHub OAuth token 不过期 (expires: 0)，无需缓存或刷新逻辑，
// 直接从账号 credentials JSONB 中读取 access_token。
type CopilotTokenProvider struct{}

func NewCopilotTokenProvider() *CopilotTokenProvider {
	return &CopilotTokenProvider{}
}

// GetAccessToken 从账号凭证中提取 GitHub OAuth access_token。
func (p *CopilotTokenProvider) GetAccessToken(_ context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformCopilot {
		return "", errors.New("not a copilot account")
	}

	accessToken := account.GetCredential("access_token")
	if accessToken == "" {
		return "", errors.New("access_token not found in credentials")
	}
	return accessToken, nil
}
