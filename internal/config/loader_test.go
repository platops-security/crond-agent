package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
api_url: https://custom.api.com
ping_key: test-key-123
timeout: 30s
max_output_bytes: 100000
log_level: debug
log_format: text
retries: 5
retry_base_delay: 500ms
`

	if err := os.WriteFile(configFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg := DefaultConfig()
	if err := loadFromFile(configFile, cfg); err != nil {
		t.Fatalf("loadFromFile failed: %v", err)
	}

	tests := []struct {
		name     string
		expected interface{}
		actual   interface{}
	}{
		{"api_url", "https://custom.api.com", cfg.APIURL},
		{"ping_key", "test-key-123", cfg.PingKey},
		{"timeout", 30 * time.Second, cfg.Timeout},
		{"max_output_bytes", 100000, cfg.MaxOutputBytes},
		{"log_level", "debug", cfg.LogLevel},
		{"log_format", "text", cfg.LogFormat},
		{"retries", 5, cfg.Retries},
		{"retry_base_delay", 500 * time.Millisecond, cfg.RetryBaseDelay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected != tt.actual {
				t.Errorf("expected %v, got %v", tt.expected, tt.actual)
			}
		})
	}
}

func TestEnvOverride(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("CROND_API_URL", "https://env.api.com")
	t.Setenv("CROND_PING_KEY", "env-key-456")
	t.Setenv("CROND_TIMEOUT", "45s")
	t.Setenv("CROND_MAX_OUTPUT_BYTES", "200000")
	t.Setenv("CROND_LOG_LEVEL", "warn")
	t.Setenv("CROND_LOG_FORMAT", "json")
	t.Setenv("CROND_RETRIES", "10")
	t.Setenv("CROND_RETRY_BASE_DELAY", "2s")
	t.Setenv("CROND_TLS_INSECURE_SKIP_VERIFY", "true")

	applyEnv(cfg)

	tests := []struct {
		name     string
		expected interface{}
		actual   interface{}
	}{
		{"api_url", "https://env.api.com", cfg.APIURL},
		{"ping_key", "env-key-456", cfg.PingKey},
		{"timeout", 45 * time.Second, cfg.Timeout},
		{"max_output_bytes", 200000, cfg.MaxOutputBytes},
		{"log_level", "warn", cfg.LogLevel},
		{"log_format", "json", cfg.LogFormat},
		{"retries", 10, cfg.Retries},
		{"retry_base_delay", 2 * time.Second, cfg.RetryBaseDelay},
		{"tls_insecure_skip_verify", true, cfg.TLSInsecureSkipVerify},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected != tt.actual {
				t.Errorf("expected %v, got %v", tt.expected, tt.actual)
			}
		})
	}
}

// TestEnvOverride_RedactPatternsNewlineSeparator pins the contract that
// regex commas (e.g. quantifiers like {1,40}) do not shred the pattern
// list. Switched from comma to newline; this test would have caught the
// regression earlier.
func TestEnvOverride_RedactPatternsNewlineSeparator(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("CROND_REDACT_PATTERNS", "Bearer .{1,40}\npostgres://[^@]+@[^/]+\n\n   trailing-with-blank   ")
	applyEnv(cfg)

	want := []string{
		"Bearer .{1,40}",
		"postgres://[^@]+@[^/]+",
		"trailing-with-blank",
	}
	if len(cfg.RedactPatterns) != len(want) {
		t.Fatalf("got %d patterns, want %d (%v)", len(cfg.RedactPatterns), len(want), cfg.RedactPatterns)
	}
	for i, p := range want {
		if cfg.RedactPatterns[i] != p {
			t.Errorf("pattern[%d] = %q, want %q", i, cfg.RedactPatterns[i], p)
		}
	}
}

func TestEnvOverride_CaptureOutputBool(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.CaptureOutput {
		t.Fatalf("default should be true")
	}
	t.Setenv("CROND_CAPTURE_OUTPUT", "false")
	applyEnv(cfg)
	if cfg.CaptureOutput {
		t.Errorf("env override should set false")
	}
}

func TestMissingConfigFile(t *testing.T) {
	// Pass empty string to use findConfigFile logic (which will find nothing)
	// This tests that Load works without an explicit config file
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with empty path should not error: %v", err)
	}

	// Should use defaults when no file exists
	if cfg.APIURL != "https://api.crond.io" {
		t.Errorf("expected default APIURL, got %s", cfg.APIURL)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log level, got %s", cfg.LogLevel)
	}
}

func TestMergePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
api_url: https://yaml.api.com
ping_key: yaml-key
timeout: 15s
`

	if err := os.WriteFile(configFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	// Set env var to override YAML
	t.Setenv("CROND_API_URL", "https://env.api.com")
	t.Setenv("CROND_TIMEOUT", "60s")
	// CROND_PING_KEY not set, should come from YAML

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Env overrides YAML
	if cfg.APIURL != "https://env.api.com" {
		t.Errorf("env should override YAML for api_url: got %s", cfg.APIURL)
	}

	if cfg.Timeout != 60*time.Second {
		t.Errorf("env should override YAML for timeout: got %v", cfg.Timeout)
	}

	// YAML value used if env not set
	if cfg.PingKey != "yaml-key" {
		t.Errorf("YAML value should be used if env not set: got %s", cfg.PingKey)
	}

	// Default used if neither YAML nor env
	if cfg.LogLevel != "info" {
		t.Errorf("default should be used if neither YAML nor env: got %s", cfg.LogLevel)
	}
}

func TestInvalidEnvValues(t *testing.T) {
	cfg := DefaultConfig()
	original := cfg.Timeout

	// Invalid duration should be ignored
	t.Setenv("CROND_TIMEOUT", "invalid-duration")
	applyEnv(cfg)
	if cfg.Timeout != original {
		t.Errorf("invalid duration should not override: got %v", cfg.Timeout)
	}

	cfg2 := DefaultConfig()
	originalMaxOutput := cfg2.MaxOutputBytes

	// Invalid int should be ignored
	t.Setenv("CROND_MAX_OUTPUT_BYTES", "not-a-number")
	applyEnv(cfg2)
	if cfg2.MaxOutputBytes != originalMaxOutput {
		t.Errorf("invalid int should not override: got %v", cfg2.MaxOutputBytes)
	}
}
