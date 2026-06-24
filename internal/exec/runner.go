// Package exec runs shell commands with configurable output caps and structured logging.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sync/atomic"
	"syscall"
	"time"
)

// Result holds the outcome of a command execution.
type Result struct {
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

// gracePeriod is the window between SIGTERM and SIGKILL when we decide to
// terminate the wrapped command (timeout, parent context cancel, or a
// forwarded signal). Long enough for a well-behaved script to flush a final
// write or roll back a transaction before being killed.
const gracePeriod = 10 * time.Second

// Options bundles the optional knobs for RunWithOptions. Zero values are
// safe defaults: no passthrough, no redaction.
//
// Note on interleaving: the child's stdout and stderr are written to
// independently. If a script emits both streams the captured Result.Stdout
// and Result.Stderr will look fine in isolation, but the combined order
// (as seen on the host or in kubectl logs) is not preserved here — that
// is inherent to splitting the streams and is the same behavior any cron
// wrapper has.
type Options struct {
	StdoutSink     io.Writer
	StderrSink     io.Writer
	RedactPatterns []*regexp.Regexp
}

// Run executes the given command args with configurable timeout and output cap.
// Returns a Result even on non-zero exit codes; only returns an error
// for exec-level failures (command not found, permission denied, etc.).
//
// Wraps RunWithOptions with default options. Backwards-compatible shim.
func Run(ctx context.Context, args []string, timeout time.Duration, maxOutput int, logger *slog.Logger) (*Result, error) {
	return RunWithOptions(ctx, args, timeout, maxOutput, Options{}, logger)
}

// RunWithSinks is like Run but additionally tees the child's stdout/stderr to
// the provided sinks (when non-nil), so the child's output remains visible to
// e.g. `kubectl logs` while still being captured into the Result for the ping
// payload. Pass nil for either sink to suppress passthrough on that stream.
//
// Backwards-compatible shim for callers that don't need redaction.
func RunWithSinks(ctx context.Context, args []string, timeout time.Duration, maxOutput int, stdoutSink, stderrSink io.Writer, logger *slog.Logger) (*Result, error) {
	return RunWithOptions(ctx, args, timeout, maxOutput, Options{
		StdoutSink: stdoutSink,
		StderrSink: stderrSink,
	}, logger)
}

// RunWithOptions is the full surface — runs the command with sinks AND
// stream-level redaction applied before the cappedWriter caps each stream.
// Redaction is line-buffered (see streamingRedactWriter) so a secret that
// straddles two Write calls is still caught, and so the cap doesn't
// inadvertently leak the head of a secret whose tail was truncated.
//
// Redaction applies only to the captured (API-bound) view. The sinks
// receive the raw bytes — operators viewing kubectl/cron logs see the
// unmodified output. This is the right asymmetry: local visibility for
// debugging, redacted view for the cross-host payload.
func RunWithOptions(ctx context.Context, args []string, timeout time.Duration, maxOutput int, opts Options, logger *slog.Logger) (*Result, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}

	logger.Info("exec start", "command", args[0], "args", args[1:], "timeout", timeout)

	// We manage cancellation manually instead of exec.CommandContext so we
	// can send SIGTERM first and allow a grace period before SIGKILL.
	// CommandContext's default behavior is an immediate SIGKILL on cancel,
	// which kills wrapped scripts mid-write under k8s pod termination.
	//
	// gosec G204: the agent's entire purpose is launching the operator's
	// configured cron command. Args are intentional input, not tainted user
	// data from a network request.
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // G204: see comment above
	// Setpgid puts the child into its own process group rooted at its PID,
	// so we can signal -PGID to reach the whole subtree (the wrapped script
	// and any helper processes it spawned).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Use capped writers to prevent OOM from large command output. The
	// streaming redactor sits BEFORE the cap so a secret that would have
	// straddled the truncation boundary gets replaced first; the cap then
	// applies to already-redacted bytes.
	stdoutCap := &cappedWriter{max: maxOutput}
	stderrCap := &cappedWriter{max: maxOutput}
	// Bound the redactor's partial-line buffer to the same cap: a newline-free
	// stream is force-flushed past maxOutput rather than held, so it can't grow
	// agent memory without limit before the cap downstream ever applies.
	stdoutCapture := newStreamingRedactWriter(stdoutCap, opts.RedactPatterns, maxOutput)
	stderrCapture := newStreamingRedactWriter(stderrCap, opts.RedactPatterns, maxOutput)

	if opts.StdoutSink != nil {
		// swallowErrWriter ensures a closed-pipe sink (e.g., user piped to
		// `head -1`) does not abort the command via io.MultiWriter's short-
		// circuit-on-error behaviour. We still want the success ping to fire
		// if the wrapped command exits 0 — losing visible output to the
		// closed sink is acceptable.
		cmd.Stdout = io.MultiWriter(stdoutCapture, swallowErrWriter{opts.StdoutSink})
	} else {
		cmd.Stdout = stdoutCapture
	}
	if opts.StderrSink != nil {
		cmd.Stderr = io.MultiWriter(stderrCapture, swallowErrWriter{opts.StderrSink})
	} else {
		cmd.Stderr = stderrCapture
	}

	// Install signal handler BEFORE forking the child. If SIGTERM lands in
	// the gap between cmd.Start and signal.Notify the default handler
	// terminates the agent and orphans the child; subscribing first
	// guarantees the signal is buffered and the helper handles it once the
	// PID is known.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		logger.Error("exec failed", "command", args[0], "error", err)
		return nil, fmt.Errorf("exec failed: %w", err)
	}
	pgid := cmd.Process.Pid

	waitErr := waitWithGracefulTermination(ctx, cmd, pgid, timeout, sigs, logger)
	duration := time.Since(start)

	// Flush any held trailing partial line through the redactor. No-op if
	// patterns is empty (in that case the writer IS the cap and Close does
	// nothing because the type assertion fails).
	flushRedactor(stdoutCapture)
	flushRedactor(stderrCapture)

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitCodeFromExitError(exitErr)
		} else {
			logger.Error("exec failed", "command", args[0], "error", waitErr)
			return nil, fmt.Errorf("exec failed: %w", waitErr)
		}
	}

	logger.Info("exec finish", "exit_code", exitCode, "duration_ms", duration.Milliseconds())

	stdoutStr := stdoutCap.String()
	stderrStr := stderrCap.String()
	if stdoutCap.truncated {
		stdoutStr += "\n...(truncated)"
	}
	if stderrCap.truncated {
		stderrStr += "\n...(truncated)"
	}

	return &Result{
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Stdout:     stdoutStr,
		Stderr:     stderrStr,
	}, nil
}

// flushRedactor flushes any partial trailing line in a streaming redactor.
// No-op for writers that are not a *streamingRedactWriter (the empty-
// patterns path where newStreamingRedactWriter returned the dst directly).
func flushRedactor(w io.Writer) {
	if r, ok := w.(*streamingRedactWriter); ok {
		_ = r.Close() //nolint:errcheck // best-effort flush
	}
}

// exitCodeFromExitError converts an *exec.ExitError into a conventional
// Unix exit code: regular exit returns 0-255 as the child reported, while
// a signal-terminated child returns 128 + signal_number (shell convention,
// so e.g. SIGTERM=15 → 143, SIGKILL=9 → 137). Without this, signaled
// processes would return Go's -1 sentinel which os.Exit truncates to 255
// and breaks `&&`-chained cron pipelines.
func exitCodeFromExitError(e *exec.ExitError) int {
	if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return e.ExitCode()
}

// waitWithGracefulTermination waits for cmd to exit while forwarding SIGTERM/
// SIGINT received by the agent to the child's process group, and enforcing
// the timeout (if > 0) by sending SIGTERM then SIGKILL after gracePeriod.
// Parent context cancellation is treated the same as a timeout. The sigs
// channel must already be subscribed (via signal.Notify) by the caller.
func waitWithGracefulTermination(ctx context.Context, cmd *exec.Cmd, pgid int, timeout time.Duration, sigs <-chan os.Signal, logger *slog.Logger) error {
	// childExited flips true the instant cmd.Wait returns. The grace-kill
	// goroutine checks it before issuing SIGKILL, closing the (very narrow)
	// race where a PID-reuse could let our SIGKILL hit an unrelated pgid
	// recycled by the kernel between reap and signal-send.
	var childExited atomic.Bool

	waitErrCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		childExited.Store(true)
		waitErrCh <- err
	}()

	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	// waitDone lets the SIGKILL-after-grace goroutine exit promptly if the
	// child terminates within the grace period.
	waitDone := make(chan struct{})
	defer close(waitDone)

	killed := false
	sendSignalAndScheduleKill := func(sig syscall.Signal) {
		_ = syscall.Kill(-pgid, sig) //nolint:errcheck // best-effort signal forward
		if killed {
			return
		}
		killed = true
		go func() {
			select {
			case <-time.After(gracePeriod):
				if childExited.Load() {
					return // PID-reuse guard: don't SIGKILL a recycled pgid
				}
				_ = syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck // best-effort
			case <-waitDone:
			}
		}()
	}

	ctxDone := ctx.Done()
	for {
		select {
		case err := <-waitErrCh:
			return err
		case sig := <-sigs:
			logger.Info("forwarding signal to child", "signal", sig.String(), "pgid", pgid)
			sendSignalAndScheduleKill(sig.(syscall.Signal))
		case <-ctxDone:
			ctxDone = nil
			logger.Warn("context cancelled, terminating child", "pgid", pgid)
			sendSignalAndScheduleKill(syscall.SIGTERM)
		case <-timeoutCh:
			timeoutCh = nil
			logger.Warn("command timeout exceeded, terminating child", "pgid", pgid, "timeout", timeout)
			sendSignalAndScheduleKill(syscall.SIGTERM)
		}
	}
}

// cappedWriter is an io.Writer that stops accepting data after max bytes.
// Prevents OOM when child processes produce excessive output.
type cappedWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.truncated {
		return len(p), nil // discard silently, report original len to avoid cmd error
	}
	remaining := w.max - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *cappedWriter) String() string {
	return w.buf.String()
}

// swallowErrWriter wraps an io.Writer to drop errors. Used inside
// io.MultiWriter so that a broken passthrough sink (closed pipe, full disk,
// detached terminal) does not propagate as a runner error.
type swallowErrWriter struct{ w io.Writer }

func (s swallowErrWriter) Write(p []byte) (int, error) {
	_, _ = s.w.Write(p)
	return len(p), nil
}
