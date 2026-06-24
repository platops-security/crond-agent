// Package config defines the agent configuration struct, defaults, and validation.
package config

import (
	"fmt"
	"net/url"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all tunable settings for the crond-agent.
type Config struct {
	APIURL                string        `yaml:"api_url"`
	PingKey               string        `yaml:"ping_key"`
	Timeout               time.Duration `yaml:"timeout"`
	MaxOutputBytes        int           `yaml:"max_output_bytes"`
	LogLevel              string        `yaml:"log_level"`
	LogFormat             string        `yaml:"log_format"`
	Retries               int           `yaml:"retries"`
	RetryBaseDelay        time.Duration `yaml:"retry_base_delay"`
	TLSCACert             string        `yaml:"tls_ca_cert"`
	TLSInsecureSkipVerify bool          `yaml:"tls_insecure_skip_verify"`
	// CaptureOutput controls whether wrapped command stdout/stderr is
	// included in the ping payload sent to the API. Default true; set
	// false for jobs whose output is privacy-sensitive.
	CaptureOutput bool `yaml:"capture_output"`
	// RedactPatterns are Go regexps applied to captured stdout/stderr
	// before sending to the API; matches are replaced with [REDACTED].
	// Validated at startup so a bad regex fails fast.
	RedactPatterns []string `yaml:"redact_patterns"`
}

// DefaultConfig returns a Config with sensible production defaults.
func DefaultConfig() *Config {
	return &Config{
		APIURL:         "https://api.crond.io",
		Timeout:        0, // no timeout
		MaxOutputBytes: 50 * 1024,
		LogLevel:       "info",
		LogFormat:      "json",
		Retries:        3,
		RetryBaseDelay: 1 * time.Second,
		CaptureOutput:  true,
	}
}

// Validate checks that required fields are set and values are within acceptable ranges.
func (c *Config) Validate() error {
	if c.APIURL == "" {
		return fmt.Errorf("config: api_url must not be empty")
	}
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("config: api_url must be a valid http(s) URL, got %q", c.APIURL)
	}
	if c.Retries < 0 {
		return fmt.Errorf("config: retries must be >= 0, got %d", c.Retries)
	}
	if c.MaxOutputBytes < 1024 {
		return fmt.Errorf("config: max_output_bytes must be >= 1024, got %d", c.MaxOutputBytes)
	}
	for i, p := range c.RedactPatterns {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("config: redact_patterns[%d] invalid regex %q: %w", i, p, err)
		}
	}
	return nil
}

// CompileRedactPatterns returns the validated regexps. Caller MUST have run
// Validate() first; we re-compile here rather than caching on the struct so
// the YAML/env-driven []string stays the single source of truth.
func (c *Config) CompileRedactPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(c.RedactPatterns))
	for _, p := range c.RedactPatterns {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}

// RedactedPingKey returns a masked version of the ping key for safe logging.
// Threshold of 12 guarantees at least 4 mystery chars between the leaked
// prefix/suffix; shorter keys collapse to "***" since 4+4 prefix+suffix
// would otherwise leak ≥ half the secret.
func (c *Config) RedactedPingKey() string {
	if len(c.PingKey) < 12 {
		return "***"
	}
	return c.PingKey[:4] + "..." + c.PingKey[len(c.PingKey)-4:]
}

// DisplayYAML returns the config as YAML with sensitive fields redacted.
// Fields are copied explicitly into a fresh struct so the caller never
// holds a Config value-copy carrying the raw PingKey alongside the
// redacted view.
func (c *Config) DisplayYAML() ([]byte, error) {
	display := struct {
		APIURL                string        `yaml:"api_url"`
		PingKey               string        `yaml:"ping_key"`
		Timeout               time.Duration `yaml:"timeout"`
		MaxOutputBytes        int           `yaml:"max_output_bytes"`
		LogLevel              string        `yaml:"log_level"`
		LogFormat             string        `yaml:"log_format"`
		Retries               int           `yaml:"retries"`
		RetryBaseDelay        time.Duration `yaml:"retry_base_delay"`
		TLSCACert             string        `yaml:"tls_ca_cert"`
		TLSInsecureSkipVerify bool          `yaml:"tls_insecure_skip_verify"`
		CaptureOutput         bool          `yaml:"capture_output"`
		RedactPatterns        []string      `yaml:"redact_patterns"`
	}{
		APIURL:                c.APIURL,
		PingKey:               c.RedactedPingKey(),
		Timeout:               c.Timeout,
		MaxOutputBytes:        c.MaxOutputBytes,
		LogLevel:              c.LogLevel,
		LogFormat:             c.LogFormat,
		Retries:               c.Retries,
		RetryBaseDelay:        c.RetryBaseDelay,
		TLSCACert:             c.TLSCACert,
		TLSInsecureSkipVerify: c.TLSInsecureSkipVerify,
		CaptureOutput:         c.CaptureOutput,
		RedactPatterns:        c.RedactPatterns,
	}
	return yaml.Marshal(display)
}
