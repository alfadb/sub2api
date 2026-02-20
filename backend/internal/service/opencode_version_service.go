package service

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type OpenCodeVersionService struct {
	version atomic.Value

	stopCh        chan struct{}
	wg            sync.WaitGroup
	checkInterval time.Duration
	httpClient    *http.Client
}

type npmRegistryResponse struct {
	Version string `json:"version"`
}

func NewOpenCodeVersionService() *OpenCodeVersionService {
	s := &OpenCodeVersionService{
		stopCh:        make(chan struct{}),
		checkInterval: 1 * time.Hour,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	s.version.Store("1.2.10")
	return s
}

func (s *OpenCodeVersionService) Start() {
	s.wg.Add(1)
	go s.refreshLoop()
	log.Println("[OpenCodeVersion] Service started")
}

func (s *OpenCodeVersionService) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	log.Println("[OpenCodeVersion] Service stopped")
}

func (s *OpenCodeVersionService) GetVersion() string {
	if v, ok := s.version.Load().(string); ok {
		return v
	}
	return "1.2.10"
}

func (s *OpenCodeVersionService) GetClientIdentifier() string {
	return "OpenCode/" + s.GetVersion()
}

func (s *OpenCodeVersionService) refreshLoop() {
	defer s.wg.Done()

	s.fetchVersion()

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.fetchVersion()
		case <-s.stopCh:
			return
		}
	}
}

func (s *OpenCodeVersionService) fetchVersion() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version := s.fetchFromNPM(ctx)
	if version != "" {
		oldVersion := s.GetVersion()
		s.version.Store(version)
		if oldVersion != version {
			log.Printf("[OpenCodeVersion] Updated from %s to %s", oldVersion, version)
		}
		return
	}

	version = s.fetchFromGitHub(ctx)
	if version != "" {
		oldVersion := s.GetVersion()
		s.version.Store(version)
		if oldVersion != version {
			log.Printf("[OpenCodeVersion] Updated from %s to %s (via GitHub)", oldVersion, version)
		}
		return
	}

	log.Printf("[OpenCodeVersion] Failed to fetch version, keeping %s", s.GetVersion())
}

func (s *OpenCodeVersionService) fetchFromNPM(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/opencode-ai/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("[OpenCodeVersion] NPM registry request failed: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[OpenCodeVersion] NPM registry returned status %d", resp.StatusCode)
		return ""
	}

	var data npmRegistryResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("[OpenCodeVersion] Failed to decode NPM response: %v", err)
		return ""
	}

	return data.Version
}

func (s *OpenCodeVersionService) fetchFromGitHub(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/sst/opencode/releases/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("[OpenCodeVersion] GitHub API request failed: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[OpenCodeVersion] GitHub API returned status %d", resp.StatusCode)
		return ""
	}

	var data struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("[OpenCodeVersion] Failed to decode GitHub response: %v", err)
		return ""
	}

	version := data.TagName
	if len(version) > 0 && version[0] == 'v' {
		version = version[1:]
	}

	return version
}
