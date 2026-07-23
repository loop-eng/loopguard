package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Budget        BudgetConfig              `yaml:"budget"`
	SpinDetection SpinDetectionConfig       `yaml:"spin_detection"`
	Enforcement   EnforcementConfig         `yaml:"enforcement"`
	Notifications NotificationConfig        `yaml:"notifications"`
	Sources       SourcesConfig             `yaml:"sources"`
	Traces        TracesConfig              `yaml:"traces"`
	Logging       LoggingConfig             `yaml:"logging"`
	Pricing       map[string]PricingOverride `yaml:"pricing"`
}

// PricingOverride allows users to override or add model pricing in config.yaml.
// Values are in USD per million tokens.
type PricingOverride struct {
	InputPerMTok      float64 `yaml:"input_per_mtok"`
	OutputPerMTok     float64 `yaml:"output_per_mtok"`
	CacheReadPerMTok  float64 `yaml:"cache_read_per_mtok"`
	CacheWritePerMTok float64 `yaml:"cache_write_per_mtok"`
}

type BudgetConfig struct {
	PerSessionUSD  float64 `yaml:"per_session_usd"`
	PerHourUSD     float64 `yaml:"per_hour_usd"`
	PerDayUSD      float64 `yaml:"per_day_usd"`
	WarnAtPercent  int     `yaml:"warn_at_percent"`
}

type SpinDetectionConfig struct {
	RepeatedCalls      int     `yaml:"repeated_calls"`
	ErrorEcho          int     `yaml:"error_echo"`
	StallMinutes       int     `yaml:"stall_minutes"`
	CostVelocityPerMin float64 `yaml:"cost_velocity_per_min"`
	ContextFillPercent int     `yaml:"context_fill_percent"`
}

type EnforcementConfig struct {
	Action           string `yaml:"action"`
	SentinelFallback bool   `yaml:"sentinel_fallback"`
}

type NotificationConfig struct {
	Desktop bool `yaml:"desktop"`
	Sound   bool `yaml:"sound"`
}

type SourcesConfig struct {
	ClaudeCode string   `yaml:"claude_code"`
	Codex      string   `yaml:"codex"`
	Gemini     string   `yaml:"gemini"`
	Custom     []string `yaml:"custom"`
}

type TracesConfig struct {
	Enabled   bool   `yaml:"enabled"`
	OutputDir string `yaml:"output_dir"`
}

type LoggingConfig struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "loopguard")
}

func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = filepath.Join(configDir(), "config.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvOverrides(cfg)
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if len(data) > 1<<20 {
		return nil, fmt.Errorf("config file too large: %d bytes", len(data))
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(cfg)
	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	defaults := Default()
	if cfg.Budget.WarnAtPercent <= 0 {
		cfg.Budget.WarnAtPercent = defaults.Budget.WarnAtPercent
	}
	if cfg.SpinDetection.RepeatedCalls <= 0 {
		cfg.SpinDetection.RepeatedCalls = defaults.SpinDetection.RepeatedCalls
	}
	if cfg.SpinDetection.ErrorEcho <= 0 {
		cfg.SpinDetection.ErrorEcho = defaults.SpinDetection.ErrorEcho
	}
	if cfg.Traces.OutputDir == "" {
		cfg.Traces.OutputDir = defaults.Traces.OutputDir
	}
	if cfg.Logging.File == "" {
		cfg.Logging.File = defaults.Logging.File
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = defaults.Logging.Level
	}
	if cfg.Logging.MaxSizeMB <= 0 {
		cfg.Logging.MaxSizeMB = defaults.Logging.MaxSizeMB
	}
	if cfg.Logging.MaxBackups <= 0 {
		cfg.Logging.MaxBackups = defaults.Logging.MaxBackups
	}
	if cfg.Logging.MaxAgeDays <= 0 {
		cfg.Logging.MaxAgeDays = defaults.Logging.MaxAgeDays
	}
}

func applyEnvOverrides(cfg *Config) {
	parseFloat := func(key string, target *float64) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			slog.Warn("invalid env var value, ignoring", "key", key, "value", v, "error", err)
			return
		}
		*target = f
	}
	parseFloat("LOOPGUARD_BUDGET_PER_SESSION", &cfg.Budget.PerSessionUSD)
	parseFloat("LOOPGUARD_BUDGET_PER_HOUR", &cfg.Budget.PerHourUSD)
	parseFloat("LOOPGUARD_BUDGET_PER_DAY", &cfg.Budget.PerDayUSD)
	if v := os.Getenv("LOOPGUARD_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

func DefaultPath() string {
	return filepath.Join(configDir(), "config.yaml")
}

// Validate checks that a loaded config has sane values.
func Validate(cfg *Config) error {
	if cfg.Budget.PerSessionUSD < 0 {
		return fmt.Errorf("budget.per_session_usd must be >= 0, got %.2f", cfg.Budget.PerSessionUSD)
	}
	if cfg.Budget.PerHourUSD < 0 {
		return fmt.Errorf("budget.per_hour_usd must be >= 0, got %.2f", cfg.Budget.PerHourUSD)
	}
	if cfg.Budget.PerDayUSD < 0 {
		return fmt.Errorf("budget.per_day_usd must be >= 0, got %.2f", cfg.Budget.PerDayUSD)
	}
	if cfg.Budget.WarnAtPercent < 0 || cfg.Budget.WarnAtPercent > 100 {
		return fmt.Errorf("budget.warn_at_percent must be 0-100, got %d", cfg.Budget.WarnAtPercent)
	}
	if cfg.SpinDetection.RepeatedCalls < 1 {
		return fmt.Errorf("spin_detection.repeated_calls must be >= 1, got %d", cfg.SpinDetection.RepeatedCalls)
	}
	if cfg.SpinDetection.ErrorEcho < 1 {
		return fmt.Errorf("spin_detection.error_echo must be >= 1, got %d", cfg.SpinDetection.ErrorEcho)
	}
	if cfg.SpinDetection.ContextFillPercent < 0 || cfg.SpinDetection.ContextFillPercent > 100 {
		return fmt.Errorf("spin_detection.context_fill_percent must be 0-100, got %d", cfg.SpinDetection.ContextFillPercent)
	}
	return nil
}

func Default() *Config {
	dir := configDir()
	return &Config{
		Budget: BudgetConfig{
			PerSessionUSD: 20.0,
			PerHourUSD:    50.0,
			PerDayUSD:     200.0,
			WarnAtPercent: 80,
		},
		SpinDetection: SpinDetectionConfig{
			RepeatedCalls:      3,
			ErrorEcho:          3,
			StallMinutes:       10,
			CostVelocityPerMin: 2.0,
			ContextFillPercent: 85,
		},
		Enforcement: EnforcementConfig{
			Action:           "pause",
			SentinelFallback: true,
		},
		Notifications: NotificationConfig{
			Desktop: true,
			Sound:   true,
		},
		Sources: SourcesConfig{
			ClaudeCode: "auto",
			Codex:      "auto",
			Gemini:     "auto",
		},
		Traces: TracesConfig{
			Enabled:   true,
			OutputDir: filepath.Join(dir, "traces"),
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       filepath.Join(dir, "loopguard.log"),
			MaxSizeMB:  50,
			MaxBackups: 3,
			MaxAgeDays: 30,
			Compress:   true,
		},
	}
}
