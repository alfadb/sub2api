package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ScriptUsageCheckService 后台定时检查脚本用量窗口
// 撞墙时自动暂停调度，窗口恢复时自动恢复
type ScriptUsageCheckService struct {
	accountRepo     AccountRepository
	usageScriptRepo UsageScriptRepository
	accountUsageSvc *AccountUsageService
	cfg             *config.ScriptUsageCheckConfig

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewScriptUsageCheckService 创建脚本用量检查服务
func NewScriptUsageCheckService(
	accountRepo AccountRepository,
	usageScriptRepo UsageScriptRepository,
	accountUsageSvc *AccountUsageService,
	cfg *config.Config,
) *ScriptUsageCheckService {
	return &ScriptUsageCheckService{
		accountRepo:     accountRepo,
		usageScriptRepo: usageScriptRepo,
		accountUsageSvc: accountUsageSvc,
		cfg:             &cfg.ScriptUsageCheck,
		stopCh:          make(chan struct{}),
	}
}

// Start 启动后台检查服务
func (s *ScriptUsageCheckService) Start() {
	if !s.cfg.Enabled {
		slog.Info("script_usage_check.service_disabled")
		return
	}

	s.wg.Add(1)
	go s.checkLoop()

	slog.Info("script_usage_check.service_started",
		"interval_seconds", s.cfg.IntervalSeconds,
	)
}

// Stop 停止检查服务
func (s *ScriptUsageCheckService) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
	slog.Info("script_usage_check.service_stopped")
}

func (s *ScriptUsageCheckService) checkLoop() {
	defer s.wg.Done()

	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = 2 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动时立即执行一次
	s.processCheck()

	for {
		select {
		case <-ticker.C:
			s.processCheck()
		case <-s.stopCh:
			return
		}
	}
}

func (s *ScriptUsageCheckService) processCheck() {
	// 限制单轮检查时间不超过检查间隔，防止慢脚本导致轮次堆叠
	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), interval)
	defer cancel()

	// 获取所有启用的脚本
	scripts, err := s.usageScriptRepo.List(ctx)
	if err != nil {
		slog.Warn("script_usage_check.list_scripts_failed", "error", err)
		return
	}

	// 过滤启用的脚本，按 host+type 建索引
	type scriptKey struct {
		host     string
		acctType string
	}
	scriptIndex := make(map[scriptKey]*UsageScript)
	for _, sc := range scripts {
		if !sc.Enabled {
			continue
		}
		scriptIndex[scriptKey{sc.BaseURLHost, sc.AccountType}] = sc
	}
	if len(scriptIndex) == 0 {
		return
	}

	// 获取所有活跃账号
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		slog.Warn("script_usage_check.list_accounts_failed", "error", err)
		return
	}

	var checked, paused, recovered int
	for i := range accounts {
		if ctx.Err() != nil {
			slog.Warn("script_usage_check.timeout", "checked", checked, "remaining", len(accounts)-i)
			break
		}

		account := &accounts[i]
		baseURL := account.GetBaseURL()
		if baseURL == "" {
			continue
		}

		host := extractBaseURLHost(baseURL)
		if _, ok := scriptIndex[scriptKey{host, account.Type}]; !ok {
			continue
		}

		checked++
		result := s.accountUsageSvc.CheckScriptUsageForScheduling(ctx, account)
		switch result {
		case ScriptCheckPaused:
			paused++
		case ScriptCheckRecovered:
			recovered++
		}
	}

	if paused > 0 || recovered > 0 {
		slog.Info("script_usage_check.completed",
			"checked", checked,
			"paused", paused,
			"recovered", recovered,
		)
	} else if checked > 0 {
		slog.Debug("script_usage_check.completed",
			"checked", checked,
		)
	}
}
