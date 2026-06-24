package logging

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLoggerJSON(t *testing.T) {
	logger := NewLogger("info", "json")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Should be able to log without panic
	logger.Info("test message", "key", "value")
	logger.Warn("warning message")
	logger.Error("error message")
}

func TestNewLoggerText(t *testing.T) {
	logger := NewLogger("info", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Should be able to log without panic
	logger.Info("test message", "key", "value")
	logger.Warn("warning message")
	logger.Error("error message")
}

func TestNewLoggerDefaultFormat(t *testing.T) {
	// Empty format should default to JSON
	logger := NewLogger("info", "")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	logger.Info("test message")
}

func TestLogLevelDebug(t *testing.T) {
	logger := NewLogger("debug", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Should allow debug level
	logger.Debug("debug message")
	logger.Info("info message")
}

func TestLogLevelError(t *testing.T) {
	logger := NewLogger("error", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Error level logger should not filter error messages
	logger.Error("error message")

	// These should not appear in output (error level filters out lower severity)
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Debug("debug message")
}

func TestLogLevelWarn(t *testing.T) {
	logger := NewLogger("warn", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	logger.Warn("warning message")
	logger.Error("error message")
}

func TestLogLevelInfo(t *testing.T) {
	logger := NewLogger("info", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")
}

func TestParseLevelDebug(t *testing.T) {
	level := parseLevel("debug")
	if level != slog.LevelDebug {
		t.Errorf("parseLevel(debug) = %v, want %v", level, slog.LevelDebug)
	}
}

func TestParseLevelWarn(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"Warning", slog.LevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := parseLevel(tt.input)
			if level != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, level, tt.want)
			}
		})
	}
}

func TestParseLevelError(t *testing.T) {
	level := parseLevel("error")
	if level != slog.LevelError {
		t.Errorf("parseLevel(error) = %v, want %v", level, slog.LevelError)
	}
}

func TestParseLevelInfo(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"", slog.LevelInfo},         // default
		{"unknown", slog.LevelInfo},  // default
		{"trace", slog.LevelInfo},    // default for unknown
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := parseLevel(tt.input)
			if level != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, level, tt.want)
			}
		})
	}
}

func TestLoggerCreation(t *testing.T) {
	tests := []struct {
		name   string
		level  string
		format string
	}{
		{"debug json", "debug", "json"},
		{"info text", "info", "text"},
		{"warn json", "warn", "json"},
		{"error text", "error", "text"},
		{"unknown level", "invalid", "json"},
		{"unknown format", "info", "yaml"},
		{"empty level", "", "text"},
		{"empty format", "info", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewLogger(tt.level, tt.format)
			if logger == nil {
				t.Fatal("expected non-nil logger")
			}

			// Should be able to use without panic
			logger.Info("test")
		})
	}
}

func TestLoggerFieldLogging(t *testing.T) {
	logger := NewLogger("info", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Should handle various field types
	logger.Info("message with fields",
		"string", "value",
		"int", 42,
		"bool", true,
		"float", 3.14,
	)
}

func TestLoggerWithGroup(t *testing.T) {
	logger := NewLogger("info", "json")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// NewLogger returns *slog.Logger which can be used with WithGroup
	grouped := logger.WithGroup("mygroup")
	if grouped == nil {
		t.Fatal("expected non-nil grouped logger")
	}

	grouped.Info("test")
}

func TestLoggerToStderr(t *testing.T) {
	// Logger should write to stderr
	logger := NewLogger("info", "text")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Just verify it doesn't panic
	logger.Info("should go to stderr")
}

func TestCaseSensitivityLevelParsing(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"dEbUg", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"Info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"Warn", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"Error", slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := parseLevel(tt.input)
			if level != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, level, tt.want)
			}
		})
	}
}

func TestLoggerCompatibility(t *testing.T) {
	logger := NewLogger("info", "json")

	// Verify it's actually *slog.Logger by checking methods exist
	// and can be called.
	logger.Log(context.TODO(), slog.LevelInfo, "test")
	logger.LogAttrs(context.TODO(), slog.LevelInfo, "test")
}

func TestLoggerBufferHandling(t *testing.T) {
	logger := NewLogger("info", "json")

	// Logging with large payloads should not panic
	largeString := strings.Repeat("x", 10000)
	logger.Info("large message", "data", largeString)
}
