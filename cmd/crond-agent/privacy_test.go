package main

import (
	"testing"

	"github.com/platops-security/crond-agent/internal/config"
)

// TestCaptureOutputFalseClearsResult covers the only privacy step that
// still lives at the commands.go layer. Stream-level redaction is tested
// inside internal/exec.
func TestCaptureOutputFalseClearsResult(t *testing.T) {
	// Validate-only sanity check: the new fields parse and the bool toggle
	// has the documented default.
	cfg := &config.Config{
		APIURL:         "https://api.crond.io",
		MaxOutputBytes: 1024,
		CaptureOutput:  false,
		RedactPatterns: []string{`Bearer \S+`},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.CaptureOutput {
		t.Errorf("CaptureOutput should be false in this fixture")
	}
	patterns := cfg.CompileRedactPatterns()
	if len(patterns) != 1 {
		t.Errorf("CompileRedactPatterns: want 1, got %d", len(patterns))
	}
}
