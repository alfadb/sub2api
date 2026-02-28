package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	opencodeGitHubReleaseURL = "https://api.github.com/repos/opencode-ai/opencode/releases/latest"
	opencodeDefaultVersion   = "1.2.13"
	opencodeRefreshInterval  = 1 * time.Hour
	opencodeHTTPTimeout      = 15 * time.Second
)

// OpenCodeVersionService fetches and caches the latest opencode release version
// from GitHub. Used to build a realistic User-Agent for Copilot requests.
type OpenCodeVersionService struct {
	mu      sync.RWMutex
	version string

	httpClient *http.Client
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func NewOpenCodeVersionService() *OpenCodeVersionService {
	return &OpenCodeVersionService{
		version:    opencodeDefaultVersion,
		httpClient: &http.Client{Timeout: opencodeHTTPTimeout},
		stopCh:     make(chan struct{}),
	}
}

// Start performs an initial fetch and starts the background refresh loop.
func (s *OpenCodeVersionService) Start() {
	if v, err := s.fetchLatestVersion(); err != nil {
		logger.L().Warn("opencode version initial fetch failed, using default",
			zap.String("default", opencodeDefaultVersion),
			zap.Error(err),
		)
	} else {
		s.mu.Lock()
		s.version = v
		s.mu.Unlock()
		logger.L().Info("opencode version fetched", zap.String("version", v))
	}

	s.wg.Add(1)
	go s.refreshLoop()
}

// Stop signals the background goroutine to exit and waits for it.
func (s *OpenCodeVersionService) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	logger.L().Info("opencode version service stopped")
}

// GetVersion returns the cached opencode version string (e.g. "1.2.13").
func (s *OpenCodeVersionService) GetVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// UserAgent returns the full User-Agent header value: "opencode/{version}".
func (s *OpenCodeVersionService) UserAgent() string {
	return "opencode/" + s.GetVersion()
}

func (s *OpenCodeVersionService) refreshLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(opencodeRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if v, err := s.fetchLatestVersion(); err != nil {
				logger.L().Warn("opencode version refresh failed, keeping previous",
					zap.String("current", s.GetVersion()),
					zap.Error(err),
				)
			} else {
				s.mu.Lock()
				old := s.version
				s.version = v
				s.mu.Unlock()
				if old != v {
					logger.L().Info("opencode version updated", zap.String("from", old), zap.String("to", v))
				}
			}
		}
	}
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func (s *OpenCodeVersionService) fetchLatestVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opencodeGitHubReleaseURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sub2api")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("github returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	tag := release.TagName
	if tag == "" {
		return "", fmt.Errorf("empty tag_name in response")
	}
	// Strip leading "v" if present (e.g. "v1.2.13" -> "1.2.13")
	if tag[0] == 'v' {
		tag = tag[1:]
	}
	return tag, nil
}
