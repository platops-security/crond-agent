package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/platops-security/crond-agent/internal/config"
	agentexec "github.com/platops-security/crond-agent/internal/exec"
	"github.com/platops-security/crond-agent/internal/ping"
)

func runExec(args []string, cfg *config.Config, logger *slog.Logger) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	key := fs.String("key", "", "ping key UUID")
	apiURL := fs.String("api-url", "", "API base URL")
	timeout := fs.Duration("timeout", 0, "command timeout (e.g. 30s, 5m)")
	passthrough := fs.Bool("passthrough-stdout", true,
		"tee child stdout/stderr to agent's stdout/stderr so `kubectl logs` and host shells see wrapped job output")
	if err := fs.Parse(args); err != nil {
		logger.Error("flag parse error", "error", err)
		os.Exit(1)
	}

	// CLI flags override config.
	if *key != "" {
		cfg.PingKey = *key
	}
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	if *timeout != 0 {
		cfg.Timeout = *timeout
	}

	// A zero timeout means no timeout: a hung wrapped job (stuck DB connection,
	// dead network mount) holds the agent forever, and cron then stacks one
	// stuck agent per tick. Warn loudly so operators set a bound.
	if cfg.Timeout == 0 {
		logger.Warn("no command timeout set — a hung job runs forever and cron will stack one stuck agent per tick; set --timeout (e.g. 30m) or `timeout:` in config")
	}

	// Re-validate now that CLI flags have been applied — startup validation
	// in main() ran against the pre-override config.
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation error: %v\n", err)
		os.Exit(1)
	}

	if cfg.PingKey == "" {
		logger.Error("--key or CROND_PING_KEY required")
		os.Exit(1)
	}

	cmdArgs := findCommandArgs(fs.Args())
	if len(cmdArgs) == 0 {
		logger.Error("no command specified after --")
		os.Exit(1)
	}

	client, err := ping.NewClient(cfg, Version, logger)
	if err != nil {
		logger.Error("client init failed", "error", err)
		os.Exit(1)
	}

	// 1. Send start ping (best-effort, bounded — start is fire-and-forget;
	// don't let a hung API stall the wrapped command).
	sendPing(client, cfg.PingKey, "start", nil, startPingTimeout, logger)

	// 2. Run command. Tee child output to host stdout/stderr when passthrough
	// is on (default) — required for `kubectl logs <pod>` to show wrapped
	// job output in K8s CronJob deployments. Runner manages its own
	// timeout/signal handling; pass a Background context.
	//
	// Redaction patterns are applied at the cappedWriter inlet — the
	// captured (API-bound) view is redacted before truncation, while the
	// host stdout/stderr sinks receive the raw bytes. This keeps the
	// operator's local view intact for debugging.
	var stdoutSink, stderrSink io.Writer
	if *passthrough {
		stdoutSink = os.Stdout
		stderrSink = os.Stderr
	}
	result, err := agentexec.RunWithOptions(context.Background(), cmdArgs, cfg.Timeout, cfg.MaxOutputBytes, agentexec.Options{
		StdoutSink:     stdoutSink,
		StderrSink:     stderrSink,
		RedactPatterns: cfg.CompileRedactPatterns(),
	}, logger)
	if err != nil {
		// Plain-stderr line so the operator reading /var/log/cron (or
		// `kubectl logs` for a CronJob) gets a readable diagnostic, not
		// just the JSON log record below.
		fmt.Fprintf(os.Stderr, "crond-agent: %v\n", err)
		logger.Error("exec failed", "error", err)
		errPayload := struct {
			Error string `json:"error"`
		}{Error: err.Error()}
		payload, _ := json.Marshal(errPayload)
		sendPing(client, cfg.PingKey, "fail", payload, finalPingTimeout, logger)
		os.Exit(1)
	}

	// Drop captured streams entirely when capture is off. Per-pattern
	// redaction already ran inline at the cappedWriter inlet.
	if !cfg.CaptureOutput {
		result.Stdout = ""
		result.Stderr = ""
	}

	// 3. Build JSON body with execution metadata.
	body, _ := json.Marshal(result)

	// 4. Send success or fail ping based on exit code. Bounded so a hung
	// API doesn't accumulate stuck agent processes across cron invocations.
	kind := "success"
	if result.ExitCode != 0 {
		kind = "fail"
	}
	sendPing(client, cfg.PingKey, kind, body, finalPingTimeout, logger)

	os.Exit(result.ExitCode)
}

// Ping timeouts. Bounding the per-call deadline prevents the agent from
// hanging when api.crond.io is unreachable, which would otherwise cause cron
// to accumulate stuck wrapper processes on every invocation.
//
// finalPingTimeout is sized so the ping client's default retry budget can
// actually fire: httpClient.Timeout=30s × ~3 attempts + brief backoffs fits
// comfortably under 90s. Reducing this further (e.g. 30s) silently disables
// retries on the final ping. The start ping is fire-and-forget; 10s caps
// the worst-case stall before the wrapped command starts.
const (
	startPingTimeout = 10 * time.Second
	finalPingTimeout = 90 * time.Second
)

// sendPing issues a single ping with a bounded deadline and logs (but does
// not propagate) any error. All ping sends in runExec go through this to
// keep error-handling consistent.
func sendPing(client *ping.Client, key, kind string, body []byte, timeout time.Duration, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Send(ctx, key, kind, body); err != nil {
		logger.Warn("ping send failed", "kind", kind, "error", err)
	}
}

func runPing(args []string, cfg *config.Config, logger *slog.Logger) {
	fs := flag.NewFlagSet("ping", flag.ExitOnError)
	key := fs.String("key", "", "ping key UUID")
	apiURL := fs.String("api-url", "", "API base URL")
	if err := fs.Parse(args); err != nil {
		logger.Error("flag parse error", "error", err)
		os.Exit(1)
	}

	if *key != "" {
		cfg.PingKey = *key
	}
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation error: %v\n", err)
		os.Exit(1)
	}

	if cfg.PingKey == "" {
		logger.Error("--key or CROND_PING_KEY required")
		os.Exit(1)
	}

	client, err := ping.NewClient(cfg, Version, logger)
	if err != nil {
		logger.Error("client init failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Send(ctx, cfg.PingKey, "", nil); err != nil {
		logger.Error("ping failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

// runConfig prints the effective merged config as YAML to stdout.
// Delegates redaction to config.DisplayYAML so the raw PingKey never sits
// in a value-copy of the full Config struct.
func runConfig(cfg *config.Config) {
	out, err := cfg.DisplayYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config marshal error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(out))
}

// findCommandArgs returns the slice after "--" separator, or all args if no separator.
func findCommandArgs(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return args
}

// runInstall copies the running crond-agent binary to --target. Used by the
// Kubernetes Helm chart's init-container pattern: the agent image is FROM
// scratch (no shell, no cp), so the binary self-copies into a shared
// emptyDir volume that the main job container then runs from.
func runInstall(args []string, logger *slog.Logger) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	target := fs.String("target", "/shared/crond-agent", "destination path for the copied binary")
	if err := fs.Parse(args); err != nil {
		logger.Error("flag parse error", "error", err)
		os.Exit(1)
	}

	src, err := os.Executable()
	if err != nil {
		logger.Error("locate running binary", "error", err)
		os.Exit(1)
	}
	n, err := installSelf(src, *target)
	if err != nil {
		logger.Error("install failed", "source", src, "target", *target, "error", err)
		os.Exit(1)
	}
	logger.Info("install ok", "source", src, "target", *target, "bytes", n)
}

// installSelf copies the binary at src to target with mode 0755. Streams
// via io.Copy rather than ReadFile + WriteFile so a kubelet starting many
// init-container pods at once doesn't allocate one binary-sized buffer per
// pod on the Go heap. Returns bytes copied for the caller to log.
func installSelf(src, target string) (int, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("read source %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return 0, fmt.Errorf("write target %s: %w", target, err)
	}
	n, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return int(n), fmt.Errorf("copy to %s: %w", target, copyErr)
	}
	if closeErr != nil {
		return int(n), fmt.Errorf("close %s: %w", target, closeErr)
	}
	return int(n), nil
}
