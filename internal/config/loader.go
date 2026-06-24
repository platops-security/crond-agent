package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load builds a Config by merging: defaults -> YAML file -> env vars.
// configPath is an explicit path from --config flag; empty means auto-search.
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	// Resolve config file path.
	path := configPath
	if path == "" {
		path = findConfigFile()
	}

	// Load YAML if a config file exists.
	if path != "" {
		if err := loadFromFile(path, cfg); err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
	}

	// Env vars override YAML values.
	applyEnv(cfg)

	return cfg, nil
}

// loadFromFile reads and unmarshals a YAML config file into cfg.
func loadFromFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

// findConfigFile searches standard locations for a config file.
// Returns empty string if none found.
//
// Precedence (first match wins):
//  1. /etc/crond-agent/config.yaml   — system-wide
//  2. ~/.config/crond-agent/config.yaml — per-user
//  3. ./config.yaml                  — working-directory override
//
// System config wins because the agent is most commonly installed via the
// distro package and called from /etc/cron.d/ — the system config is the
// expected source of truth. Operators wanting to override per-shell can
// pass --config or set CROND_* env vars (loaded after this search).
func findConfigFile() string {
	candidates := []string{
		"./config.yaml",
	}

	// User config dir: ~/.config/crond-agent/config.yaml
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{
			filepath.Join(home, ".config", "crond-agent", "config.yaml"),
		}, candidates...)
	}

	// System config: /etc/crond-agent/config.yaml (highest precedence).
	candidates = append([]string{"/etc/crond-agent/config.yaml"}, candidates...)

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// applyEnv overlays environment variables onto the config.
func applyEnv(cfg *Config) {
	if v := os.Getenv("CROND_API_URL"); v != "" {
		cfg.APIURL = v
	}
	if v := os.Getenv("CROND_PING_KEY"); v != "" {
		cfg.PingKey = v
	}
	if v := os.Getenv("CROND_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	if v := os.Getenv("CROND_MAX_OUTPUT_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxOutputBytes = n
		}
	}
	if v := os.Getenv("CROND_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("CROND_LOG_FORMAT"); v != "" {
		cfg.LogFormat = v
	}
	if v := os.Getenv("CROND_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retries = n
		}
	}
	if v := os.Getenv("CROND_RETRY_BASE_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.RetryBaseDelay = d
		}
	}
	if v := os.Getenv("CROND_TLS_CA_CERT"); v != "" {
		cfg.TLSCACert = v
	}
	if v := os.Getenv("CROND_TLS_INSECURE_SKIP_VERIFY"); v != "" {
		cfg.TLSInsecureSkipVerify, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("CROND_CAPTURE_OUTPUT"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CaptureOutput = b
		}
	}
	if v := os.Getenv("CROND_REDACT_PATTERNS"); v != "" {
		// Newline-separated list of regexes. Newline is chosen over comma
		// because commas are valid inside regex quantifiers (e.g. `.{1,40}`)
		// and would otherwise shred patterns silently. Shell users can
		// supply multi-line values with $'pat1\npat2' or quoted strings
		// containing literal newlines; in YAML/Kubernetes manifests use
		// `value: |- pat1\npat2`.
		parts := strings.Split(v, "\n")
		patterns := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				patterns = append(patterns, p)
			}
		}
		cfg.RedactPatterns = patterns
	}
}
