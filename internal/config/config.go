package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Accounts  []Account       `yaml:"accounts"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Log       LogConfig       `yaml:"log"`
}

type ServerConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout int    `yaml:"timeout"` // 超时时间（秒）
}

type Account struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  bool   `yaml:"enabled"`
}

type SchedulerConfig struct {
	Cron        string `yaml:"cron"`
	Timezone    string `yaml:"timezone"`
	RandomDelay *int   `yaml:"random_delay"`
}

type LogConfig struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置默认值
	if cfg.Server.Timeout == 0 {
		cfg.Server.Timeout = 30
	}
	if cfg.Scheduler.Timezone == "" {
		cfg.Scheduler.Timezone = "Asia/Shanghai"
	}
	if cfg.Scheduler.RandomDelay == nil {
		defaultDelay := 30 // 默认 30 分钟随机延迟
		cfg.Scheduler.RandomDelay = &defaultDelay
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}

	// 注意：YAML 解析时，如果 enabled 字段不存在，bool 默认为 false
	// 无法区分"未设置"和"设置为 false"，建议在配置文件中明确设置 enabled 字段

	return &cfg, nil
}

// GetTimeout 获取超时时间
func (s *ServerConfig) GetTimeout() time.Duration {
	return time.Duration(s.Timeout) * time.Second
}

// GetLocation 获取时区
func (s *SchedulerConfig) GetLocation() (*time.Location, error) {
	return time.LoadLocation(s.Timezone)
}
