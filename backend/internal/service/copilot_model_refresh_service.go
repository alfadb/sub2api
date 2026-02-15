package service

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type CopilotModelRefreshService struct {
	accountRepo        AccountRepository
	githubCopilotToken *GitHubCopilotTokenProvider
	cfg                *config.CopilotModelRefreshConfig

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewCopilotModelRefreshService(accountRepo AccountRepository, githubCopilotToken *GitHubCopilotTokenProvider, cfg *config.Config) *CopilotModelRefreshService {
	var c *config.CopilotModelRefreshConfig
	if cfg != nil {
		c = &cfg.CopilotModelRefresh
	}
	return &CopilotModelRefreshService{
		accountRepo:        accountRepo,
		githubCopilotToken: githubCopilotToken,
		cfg:                c,
		stopCh:             make(chan struct{}),
	}
}

func (s *CopilotModelRefreshService) Start() {
	if s == nil || s.cfg == nil || !s.cfg.Enabled {
		return
	}
	if s.githubCopilotToken == nil {
		log.Println("[CopilotModelRefresh] GitHub Copilot token provider is nil")
		return
	}

	s.wg.Add(1)
	go s.refreshLoop()

	log.Printf("[CopilotModelRefresh] Service started (check every %d minutes)", s.cfg.CheckIntervalMinutes)
}

func (s *CopilotModelRefreshService) Stop() {
	if s == nil {
		return
	}
	close(s.stopCh)
	s.wg.Wait()
	log.Println("[CopilotModelRefresh] Service stopped")
}

func (s *CopilotModelRefreshService) refreshLoop() {
	defer s.wg.Done()

	interval := time.Duration(s.cfg.CheckIntervalMinutes) * time.Minute
	if interval < time.Minute {
		interval = 6 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.processRefresh()

	for {
		select {
		case <-ticker.C:
			s.processRefresh()
		case <-s.stopCh:
			return
		}
	}
}

func (s *CopilotModelRefreshService) processRefresh() {
	ctx := context.Background()

	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		log.Printf("[CopilotModelRefresh] Failed to list accounts: %v", err)
		return
	}

	refreshed, failed, skipped := 0, 0, 0

	for i := range accounts {
		acc := &accounts[i]
		if !isGitHubCopilotAccount(acc) {
			skipped++
			continue
		}

		timeout := time.Duration(s.cfg.RequestTimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		models, fetchErr := s.githubCopilotToken.ListModels(reqCtx, acc)
		cancel()

		now := time.Now().Format(time.RFC3339)
		if fetchErr != nil || len(models) == 0 {
			failed++
			msg := ""
			if fetchErr != nil {
				msg = fetchErr.Error()
			}
			_ = s.accountRepo.UpdateExtra(ctx, acc.ID, map[string]any{
				AccountExtraKeyAvailableModelsSource:  "github_copilot",
				AccountExtraKeyAvailableModelsError:   msg,
				AccountExtraKeyAvailableModelsErrorAt: now,
			})
			continue
		}

		ids := make([]string, 0, len(models))
		for _, m := range models {
			if id := strings.TrimSpace(m.ID); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			failed++
			_ = s.accountRepo.UpdateExtra(ctx, acc.ID, map[string]any{
				AccountExtraKeyAvailableModelsSource:  "github_copilot",
				AccountExtraKeyAvailableModelsError:   "copilot models response contained no model ids",
				AccountExtraKeyAvailableModelsErrorAt: now,
			})
			continue
		}

		refreshed++
		_ = s.accountRepo.UpdateExtra(ctx, acc.ID, map[string]any{
			AccountExtraKeyAvailableModels:          ids,
			AccountExtraKeyAvailableModelsUpdatedAt: now,
			AccountExtraKeyAvailableModelsSource:    "github_copilot",
			AccountExtraKeyAvailableModelsError:     "",
			AccountExtraKeyAvailableModelsErrorAt:   "",
		})
	}

	log.Printf("[CopilotModelRefresh] Cycle complete: refreshed=%d failed=%d skipped=%d", refreshed, failed, skipped)
}
