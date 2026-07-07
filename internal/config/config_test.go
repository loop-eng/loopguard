package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()

	if cfg.Budget.PerSessionUSD != 20.0 {
		t.Errorf("PerSessionUSD = %v, want 20.0", cfg.Budget.PerSessionUSD)
	}
	if cfg.Budget.PerHourUSD != 50.0 {
		t.Errorf("PerHourUSD = %v, want 50.0", cfg.Budget.PerHourUSD)
	}
	if cfg.Budget.PerDayUSD != 200.0 {
		t.Errorf("PerDayUSD = %v, want 200.0", cfg.Budget.PerDayUSD)
	}
	if cfg.Budget.WarnAtPercent != 80 {
		t.Errorf("WarnAtPercent = %v, want 80", cfg.Budget.WarnAtPercent)
	}
	if cfg.SpinDetection.RepeatedCalls != 3 {
		t.Errorf("RepeatedCalls = %v, want 3", cfg.SpinDetection.RepeatedCalls)
	}
	if cfg.Enforcement.Action != "pause" {
		t.Errorf("Action = %v, want pause", cfg.Enforcement.Action)
	}
	if !cfg.Notifications.Desktop {
		t.Error("Desktop notifications should be enabled by default")
	}
	if !cfg.Traces.Enabled {
		t.Error("Traces should be enabled by default")
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load should not error on missing file, got: %v", err)
	}
	if cfg.Budget.PerSessionUSD != 20.0 {
		t.Error("should return defaults when file is missing")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `
budget:
  per_session_usd: 50.0
  per_hour_usd: 100.0
spin_detection:
  repeated_calls: 5
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Budget.PerSessionUSD != 50.0 {
		t.Errorf("PerSessionUSD = %v, want 50.0", cfg.Budget.PerSessionUSD)
	}
	if cfg.Budget.PerHourUSD != 100.0 {
		t.Errorf("PerHourUSD = %v, want 100.0", cfg.Budget.PerHourUSD)
	}
	// Unset fields should keep defaults
	if cfg.Budget.PerDayUSD != 200.0 {
		t.Errorf("PerDayUSD = %v, want 200.0 (default)", cfg.Budget.PerDayUSD)
	}
	if cfg.SpinDetection.RepeatedCalls != 5 {
		t.Errorf("RepeatedCalls = %v, want 5", cfg.SpinDetection.RepeatedCalls)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("should error on invalid YAML")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("LOOPGUARD_BUDGET_PER_SESSION", "99.5")
	t.Setenv("LOOPGUARD_LOG_LEVEL", "debug")

	cfg, _ := Load("/nonexistent")

	if cfg.Budget.PerSessionUSD != 99.5 {
		t.Errorf("PerSessionUSD = %v, want 99.5 (from env)", cfg.Budget.PerSessionUSD)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Level = %v, want debug (from env)", cfg.Logging.Level)
	}
}
