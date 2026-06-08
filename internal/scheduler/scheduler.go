package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/INKCR0W/jm-automation/internal/api"
	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/internal/task"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	"github.com/INKCR0W/jm-automation/pkg/utils"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron           *cron.Cron
	config         *config.Config
	clientMap      map[string]*client.Client
	baseURLs       []string
	delayGenerator func(maxMinutes int) time.Duration
	mu             sync.Mutex
}

func New(cfg *config.Config) (*Scheduler, error) {
	// 加载时区
	loc, err := cfg.Scheduler.GetLocation()
	if err != nil {
		return nil, fmt.Errorf("加载时区失败: %w", err)
	}

	// 创建定时任务，设置时区
	cronInstance := cron.New(cron.WithSeconds(), cron.WithLocation(loc))

	baseURLs := api.ResolveDynamicBaseURLs(context.Background())
	if cfg.Server.BaseURL != "" {
		baseURLs = append([]string{cfg.Server.BaseURL}, baseURLs...)
	}
	baseURLs = uniqueBaseURLs(baseURLs)
	logger.Info("API 域名候选加载完成", "count", len(baseURLs), "primary", baseURLs[0])

	return &Scheduler{
		cron:           cronInstance,
		config:         cfg,
		clientMap:      make(map[string]*client.Client),
		baseURLs:       baseURLs,
		delayGenerator: randomDelayDuration,
	}, nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	// 添加定时任务
	_, err := s.cron.AddFunc(s.config.Scheduler.Cron, func() {
		// 添加随机延迟
		if s.config.Scheduler.RandomDelay != nil && *s.config.Scheduler.RandomDelay > 0 {
			delay := s.getRandomDelay()
			logger.Info("添加随机延迟", "delay", delay)
			time.Sleep(delay)
		}

		// 每次执行时创建新的 context，避免使用已取消的 context
		taskCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if err := s.RunOnce(taskCtx); err != nil {
			logger.Error("定时任务执行失败", "error", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加定时任务失败: %w", err)
	}

	s.cron.Start()
	return nil
}

// getRandomDelay 获取随机延迟时间
func (s *Scheduler) getRandomDelay() time.Duration {
	if s.config.Scheduler.RandomDelay == nil {
		return 0
	}

	maxMinutes := *s.config.Scheduler.RandomDelay
	if maxMinutes <= 0 {
		return 0
	}

	generateDelay := s.delayGenerator
	if generateDelay == nil {
		generateDelay = randomDelayDuration
	}
	return generateDelay(maxMinutes)
}

func randomDelayDuration(maxMinutes int) time.Duration {
	// 使用 utils.RandomInt 生成真正的随机分钟数（0 到 maxMinutes 之间）
	randomMinutes := utils.RandomInt(0, maxMinutes+1)
	return time.Duration(randomMinutes) * time.Minute
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		s.cron.Stop()
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(s.config.Accounts))

	for _, account := range s.config.Accounts {
		if !account.Enabled {
			logger.Info("账号已禁用，跳过", "username", account.Username)
			continue
		}

		wg.Add(1)
		go func(acc config.Account) {
			defer wg.Done()

			c, err := s.getOrCreateClient(acc.Username)
			if err != nil {
				logger.Error("获取客户端失败", "username", acc.Username, "error", err)
				errCh <- err
				return
			}

			executor := task.NewExecutor(c)
			if err := executor.Execute(ctx, acc); err != nil {
				logger.Error("账号任务执行失败", "username", acc.Username, "error", err)
				errCh <- err
			}
		}(account)
	}

	wg.Wait()
	close(errCh)

	// 收集错误
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("部分账号执行失败，错误数: %d", len(errs))
	}

	return nil
}

func (s *Scheduler) getOrCreateClient(username string) (*client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, exists := s.clientMap[username]; exists {
		return c, nil
	}

	c, err := client.New(s.config.Server.BaseURL, s.config.Server.GetTimeout(), username)
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %w", err)
	}
	c.SetBaseURLs(s.baseURLs)
	if err := c.LoadCookies(); err != nil {
		logger.Debug("使用完整域名候选重新加载 cookies 失败", "username", username, "error", err)
	}

	s.clientMap[username] = c
	logger.Info("为账号创建新的客户端实例", "username", username)

	return c, nil
}

func uniqueBaseURLs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		// 这里只做兼容性规范化，不拒绝非法 URL；配置校验如需收紧，应单独改造 New 的错误返回契约。
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		out = []string{api.DefaultBaseURL}
	}
	return out
}
