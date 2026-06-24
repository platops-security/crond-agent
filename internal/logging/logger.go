// Package logging provides a centralized slog factory for the crond-agent.
// JSON format (default) for production; text format for local dev.
// Output goes to stderr so stdout is reserved for command output in exec mode.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Logger wraps slog.Logger for structured logging across agent components.
type Logger = slog.Logger

// NewLogger creates a structured logger with the given level and format.
// format: "json" (default) or "text". level: "debug", "info", "warn", "error".
func NewLogger(level, format string) *Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}

// parseLevel maps a string log level name to the corresponding slog.Level.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
