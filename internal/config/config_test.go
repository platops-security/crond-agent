package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name     string
		field    string
		expected interface{}
		actual   interface{}
	}{
		{"APIURL", "APIURL", "https://api.crond.io", cfg.APIURL},
		{"Timeout", "Timeout", time.Duration(0), cfg.Timeout},
		{"MaxOutputBytes", "MaxOutputBytes", 50 * 1024, cfg.MaxOutputBytes},
		{"LogLevel", "LogLevel", "info", cfg.LogLevel},
		{"LogFormat", "LogFormat", "json", cfg.LogFormat},
		{"Retries", "Retries", 3, cfg.Retries},
		{"RetryBaseDelay", "RetryBaseDelay", 1 * time.Second, cfg.RetryBaseDelay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected != tt.actual {
				t.Errorf("field %s: expected %v, got %v", tt.field, tt.expected, tt.actual)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		wantError bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 50 * 1024,
				Retries:        3,
			},
			wantError: false,
		},
		{
			name: "empty api_url",
			config: &Config{
				APIURL:         "",
				MaxOutputBytes: 50 * 1024,
				Retries:        3,
			},
			wantError: true,
			errMsg:    "api_url must not be empty",
		},
		{
			name: "negative retries",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 50 * 1024,
				Retries:        -1,
			},
			wantError: true,
			errMsg:    "retries must be >= 0",
		},
		{
			name: "zero retries is valid",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 50 * 1024,
				Retries:        0,
			},
			wantError: false,
		},
		{
			name: "max_output_bytes too low",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 1023,
				Retries:        3,
			},
			wantError: true,
			errMsg:    "max_output_bytes must be >= 1024",
		},
		{
			name: "max_output_bytes at minimum",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 1024,
				Retries:        3,
			},
			wantError: false,
		},
		{
			name: "api_url missing scheme",
			config: &Config{
				APIURL:         "api.crond.io",
				MaxOutputBytes: 1024,
				Retries:        3,
			},
			wantError: true,
			errMsg:    "api_url must be a valid http(s) URL",
		},
		{
			name: "api_url file scheme rejected",
			config: &Config{
				APIURL:         "file:///etc/passwd",
				MaxOutputBytes: 1024,
				Retries:        3,
			},
			wantError: true,
			errMsg:    "api_url must be a valid http(s) URL",
		},
		{
			name: "api_url missing host",
			config: &Config{
				APIURL:         "https://",
				MaxOutputBytes: 1024,
				Retries:        3,
			},
			wantError: true,
			errMsg:    "api_url must be a valid http(s) URL",
		},
		{
			name: "api_url http (insecure) accepted",
			config: &Config{
				APIURL:         "http://localhost:8080",
				MaxOutputBytes: 1024,
				Retries:        3,
			},
			wantError: false,
		},
		{
			name: "bad redact pattern fails validation",
			config: &Config{
				APIURL:         "https://api.crond.io",
				MaxOutputBytes: 1024,
				Retries:        3,
				RedactPatterns: []string{"(unclosed"},
			},
			wantError: true,
			errMsg:    "redact_patterns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantError && err != nil && !contains(err.Error(), tt.errMsg) {
				t.Errorf("error message %q does not contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

// TestDisplayYAMLCoversAllConfigFields guards against future drift where a
// new Config field is added but DisplayYAML's anonymous struct forgets to
// mirror it — silently absent from `crond-agent config` output. We compare
// the set of yaml-tag names in Config vs. those present in DisplayYAML's
// rendered output for a fully-populated fixture.
func TestDisplayYAMLCoversAllConfigFields(t *testing.T) {
	cfg := &Config{
		APIURL:                "https://api.example.com",
		PingKey:               "abcdefghijklmnop", // > 12 so redaction prints
		Timeout:               5 * time.Second,
		MaxOutputBytes:        1024,
		LogLevel:              "info",
		LogFormat:             "json",
		Retries:               3,
		RetryBaseDelay:        1 * time.Second,
		TLSCACert:             "/etc/ssl/ca.pem",
		TLSInsecureSkipVerify: true,
		CaptureOutput:         true,
		RedactPatterns:        []string{`Bearer \S+`},
	}
	out, err := cfg.DisplayYAML()
	if err != nil {
		t.Fatalf("DisplayYAML: %v", err)
	}

	// Every yaml-tagged field on Config should appear as a key in the
	// rendered output. If you add a field to Config, update DisplayYAML
	// and this list together.
	cfgT := reflect.TypeOf(*cfg)
	for i := 0; i < cfgT.NumField(); i++ {
		tag := cfgT.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		// yaml tag may have ",omitempty"; take the name part.
		name, _, _ := strings.Cut(tag, ",")
		if !strings.Contains(string(out), name+":") {
			t.Errorf("DisplayYAML output missing field %q — anonymous struct in DisplayYAML must be updated", name)
		}
	}
}

func TestRedactedPingKey(t *testing.T) {
	tests := []struct {
		name     string
		pingKey  string
		expected string
	}{
		{"empty key", "", "***"},
		{"1 char", "a", "***"},
		{"4 chars", "abcd", "***"},
		{"8 chars", "abcdefgh", "***"},
		{"9 chars", "abcdefghi", "***"},
		{"11 chars", "abcdefghijk", "***"},
		{"12 chars", "abcdefghijkl", "abcd...ijkl"},
		{"20 chars", "abcdefghij1234567890", "abcd...7890"},
		{"long key", "verylongpingkeythathasmanycharacters", "very...ters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{PingKey: tt.pingKey}
			got := cfg.RedactedPingKey()
			if got != tt.expected {
				t.Errorf("RedactedPingKey() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
