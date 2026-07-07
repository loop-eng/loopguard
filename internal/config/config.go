package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Budget        BudgetConfig        `yaml:"budget"`
	SpinDetection SpinDetectionConfig `yaml:"spin_detection"`
	Enforcement   EnforcementConfig   `yaml:"enforcement"`
	Notifications NotificationConfig  `yaml:"notifications"`
	Sources       SourcesConfig       `yaml:"sources"`
	Traces        TracesConfig        `yaml:"traces"`
	Logging       LoggingConfig       `yaml:"logging"`
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
	Level string `yaml:"level"`
	File  string `yaml:"file"`
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

	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LOOPGUARD_BUDGET_PER_SESSION"); v != "" {
		fmt.Sscanf(v, "%f", &cfg.Budget.PerSessionUSD)
	}
	if v := os.Getenv("LOOPGUARD_BUDGET_PER_HOUR"); v != "" {
		fmt.Sscanf(v, "%f", &cfg.Budget.PerHourUSD)
	}
	if v := os.Getenv("LOOPGUARD_BUDGET_PER_DAY"); v != "" {
		fmt.Sscanf(v, "%f", &cfg.Budget.PerDayUSD)
	}
	if v := os.Getenv("LOOPGUARD_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

func DefaultPath() string {
	return filepath.Join(configDir(), "config.yaml")
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
			Level: "info",
			File:  filepath.Join(dir, "loopguard.log"),
		},
	}
}
