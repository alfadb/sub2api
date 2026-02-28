package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const defaultCopilotUsageURL = "https://api.github.com/copilot_internal/user"

type copilotUsageService struct {
	usageURL     string
	httpUpstream service.HTTPUpstream
}

// NewCopilotUsageFetcher 创建 Copilot 用量获取服务
func NewCopilotUsageFetcher(httpUpstream service.HTTPUpstream) service.CopilotUsageFetcher {
	return &copilotUsageService{
		usageURL:     defaultCopilotUsageURL,
		httpUpstream: httpUpstream,
	}
}

// FetchUsage 获取 Copilot 用量数据
func (s *copilotUsageService) FetchUsage(ctx context.Context, accessToken, proxyURL string, accountID int64) (*service.CopilotUsageResponse, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("access_token is empty")
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", s.usageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// 设置请求头（参考 copilot-api 实现）
	req.Header.Set("authorization", "token "+accessToken)
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", "GitHubCopilotChat/1.0.0")
	req.Header.Set("editor-version", "vscode/1.0.0")
	req.Header.Set("editor-plugin-version", "copilot-chat/1.0.0")
	req.Header.Set("x-github-api-version", "2025-10-01")

	var resp *http.Response

	// 使用 HTTPUpstream 发送请求（支持代理）
	if s.httpUpstream != nil {
		resp, err = s.httpUpstream.Do(req, proxyURL, accountID, 0)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
	} else {
		// 回退到标准 HTTP 客户端
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// 401/403 返回空 usage，不中断服务
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &service.CopilotUsageResponse{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var usageResp service.CopilotUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &usageResp, nil
}
