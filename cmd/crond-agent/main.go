package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/platops-security/crond-agent/internal/config"
	"github.com/platops-security/crond-agent/internal/logging"
)

// Version, Commit, BuildDate are injected at build time via -ldflags.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	// Parse global --config flag before subcommand dispatch.
	// Only scan args before the first recognized subcommand or "--" to avoid
	// consuming args meant for the child command.
	configPath, args := extractConfigFlag(os.Args[1:])

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation error: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewLogger(cfg.LogLevel, cfg.LogFormat)

	switch args[0] {
	case "exec":
		runExec(args[1:], cfg, logger)
	case "ping":
		runPing(args[1:], cfg, logger)
	case "config":
		runConfig(cfg)
	case "install":
		runInstall(args[1:], logger)
	case "version":
		fmt.Printf("crond-agent %s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// extractConfigFlag scans args for --config before any subcommand or "--".
// Returns the config path (empty if not found) and the args with the flag
// removed. The caller's slice is never mutated; we always return a fresh
// slice when we remove an element so a later os.Args inspection sees the
// original ordering.
func extractConfigFlag(args []string) (string, []string) {
	subcommands := map[string]bool{"exec": true, "ping": true, "config": true, "install": true, "version": true, "help": true}

	for i, a := range args {
		// Stop scanning at subcommand or "--".
		if subcommands[a] || a == "--" {
			break
		}
		if a == "--config" && i+1 < len(args) {
			return args[i+1], removeAt(args, i, i+2)
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config="), removeAt(args, i, i+1)
		}
	}
	return "", args
}

// removeAt returns a new slice with args[from:to] elided. Equivalent to
// append(args[:from], args[to:]...) but allocates fresh storage instead
// of mutating the underlying array.
func removeAt(args []string, from, to int) []string {
	out := make([]string, 0, len(args)-(to-from))
	out = append(out, args[:from]...)
	out = append(out, args[to:]...)
	return out
}

func printUsage() {
	fmt.Print(`crond-agent - cron job monitoring agent

Usage:
  crond-agent [--config path] <command> [flags]

Commands:
  exec [flags] -- <command> [args...]   Wrap and monitor a command
  ping [flags]                          Send a simple ping
  config                                Print effective config (YAML)
  install [--target path]               Self-copy the binary to <target> (default /shared/crond-agent)
                                        Used by the K8s Helm chart init-container pattern.
  version                               Print version info

Global flags:
  --config    Path to config file (default: auto-search)

Exec/Ping flags:
  --key       Ping key UUID (or CROND_PING_KEY env)
  --api-url   API base URL (or CROND_API_URL env)
  --timeout   Command timeout (exec only, e.g. 30s, 5m)

Privacy env vars (exec only):
  CROND_CAPTURE_OUTPUT   "false" drops captured stdout/stderr from the ping payload
  CROND_REDACT_PATTERNS  comma-separated regexes; matches replaced with [REDACTED]
                         before send. Validated at startup.

Examples:
  crond-agent exec --key abc-123 -- /usr/bin/backup.sh
  crond-agent ping --key abc-123
  crond-agent --config /etc/crond-agent/config.yaml exec -- ./script.sh
`)
}
