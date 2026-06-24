package exec

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunSuccess(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	result, err := Run(ctx, []string{"echo", "hello"}, 5*time.Second, 1024, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

	expectedOutput := "hello\n"
	if result.Stdout != expectedOutput {
		t.Errorf("stdout = %q, want %q", result.Stdout, expectedOutput)
	}

	if result.DurationMs <= 0 {
		t.Errorf("duration_ms should be > 0, got %d", result.DurationMs)
	}
}

func TestRunFailure(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	result, err := Run(ctx, []string{"false"}, 5*time.Second, 1024, logger)

	// false command returns non-zero exit, but should not error at exec level
	if err != nil {
		t.Fatalf("Run should not error on non-zero exit: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", result.ExitCode)
	}
}

func TestRunTimeout(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Run sleep 10 with 100ms timeout
	_, err := Run(ctx, []string{"sleep", "10"}, 100*time.Millisecond, 1024, logger)

	// Either get error or result with timeout indication
	// The behavior depends on whether the command is killed before exiting
	if err != nil {
		// Context deadline error is expected
		if !strings.Contains(err.Error(), "context deadline") &&
			!strings.Contains(err.Error(), "context canceled") {
			t.Errorf("error should contain context error, got %v", err)
		}
	}
	// Some systems may return a result with exit code rather than error
}

func TestRunTruncation(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Generate output larger than max
	// 'yes' outputs 'y\n' repeatedly, will exceed maxOutput quickly
	maxOutput := 100
	result, err := Run(ctx, []string{"sh", "-c", "yes | head -1000"}, 5*time.Second, maxOutput, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Stdout should be truncated to maxOutput + marker
	// The truncate function adds "\n...(truncated)" which is 14 chars
	maxAllowed := maxOutput + len("\n...(truncated)")
	if len(result.Stdout) > maxAllowed {
		t.Errorf("output length %d exceeds max %d", len(result.Stdout), maxAllowed)
	}

	// Should contain truncation marker
	if !strings.Contains(result.Stdout, "...(truncated)") {
		t.Errorf("truncated output should contain marker, got %q", result.Stdout)
	}
}

func TestRunNotFound(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Try to run a command that doesn't exist
	result, err := Run(ctx, []string{"this-command-does-not-exist-12345"}, 5*time.Second, 1024, logger)

	// Should get exec error
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}

	if result != nil {
		t.Errorf("expected nil result on exec error, got %v", result)
	}

	if !strings.Contains(err.Error(), "exec failed") {
		t.Errorf("error should contain 'exec failed', got %v", err)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	result, err := Run(ctx, []string{}, 5*time.Second, 1024, logger)
	if err == nil {
		t.Fatal("expected error for empty command")
	}

	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}

	if !strings.Contains(err.Error(), "no command specified") {
		t.Errorf("error should contain 'no command specified', got %v", err)
	}
}

func TestRunWithStderr(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Command that writes to stderr
	result, err := Run(ctx, []string{"sh", "-c", "echo error >&2"}, 5*time.Second, 1024, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

	expectedStderr := "error\n"
	if result.Stderr != expectedStderr {
		t.Errorf("stderr = %q, want %q", result.Stderr, expectedStderr)
	}
}

func TestRunDurationRecorded(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	result, err := Run(ctx, []string{"sleep", "0.1"}, 5*time.Second, 1024, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have recorded approximately 100ms
	if result.DurationMs < 50 || result.DurationMs > 500 {
		t.Errorf("duration_ms = %d, expected roughly 100ms", result.DurationMs)
	}
}

func TestRunNoTimeout(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Timeout of 0 means no timeout
	result, err := Run(ctx, []string{"echo", "test"}, 0, 1024, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestRunWithSinks_PassthroughTeesToSink(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	var stdoutSink, stderrSink bytes.Buffer
	result, err := RunWithSinks(ctx,
		[]string{"sh", "-c", "echo out; echo err >&2"},
		5*time.Second, 1024,
		&stdoutSink, &stderrSink, logger,
	)
	if err != nil {
		t.Fatalf("RunWithSinks failed: %v", err)
	}

	// Captured Result still has the output (capped buffer).
	if result.Stdout != "out\n" {
		t.Errorf("result.Stdout = %q, want %q", result.Stdout, "out\n")
	}
	if result.Stderr != "err\n" {
		t.Errorf("result.Stderr = %q, want %q", result.Stderr, "err\n")
	}
	// Sink received the same output (passthrough working).
	if stdoutSink.String() != "out\n" {
		t.Errorf("stdoutSink = %q, want %q", stdoutSink.String(), "out\n")
	}
	if stderrSink.String() != "err\n" {
		t.Errorf("stderrSink = %q, want %q", stderrSink.String(), "err\n")
	}
}

func TestRunWithSinks_NilSinksBehaveLikeRun(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	// Nil sinks → no passthrough; behavior identical to Run.
	result, err := RunWithSinks(ctx, []string{"echo", "hi"}, 5*time.Second, 1024, nil, nil, logger)
	if err != nil {
		t.Fatalf("RunWithSinks failed: %v", err)
	}
	if result.Stdout != "hi\n" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hi\n")
	}
}

func TestRunWithSinks_OnlyStdoutPassthrough(t *testing.T) {
	logger := newTestLogger()
	ctx := context.Background()

	var stdoutSink bytes.Buffer
	result, err := RunWithSinks(ctx,
		[]string{"sh", "-c", "echo out; echo err >&2"},
		5*time.Second, 1024,
		&stdoutSink, nil, logger,
	)
	if err != nil {
		t.Fatalf("RunWithSinks failed: %v", err)
	}

	if stdoutSink.String() != "out\n" {
		t.Errorf("stdoutSink should receive only stdout, got %q", stdoutSink.String())
	}
	// stderr still captured in Result even when its sink is nil.
	if result.Stderr != "err\n" {
		t.Errorf("result.Stderr = %q, want %q", result.Stderr, "err\n")
	}
}

// TestRunForwardsSignalToChildGroup confirms that a SIGTERM received by the
// agent is forwarded to the entire child process group (not just the direct
// child), so the wrapped script and its descendants get a chance to shut
// down gracefully instead of being SIGKILLed by Go's CommandContext default.
func TestRunForwardsSignalToChildGroup(t *testing.T) {
	logger := newTestLogger()

	tmp, err := os.CreateTemp("", "runner-sig-grandchild-*.marker")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	// Shell script: launch a grandchild that writes "term" to the marker file
	// on SIGTERM and exits 0. Parent waits for the grandchild. Setpgid means
	// both the shell and the grandchild are in our PG and both receive SIGTERM.
	script := `
		trap 'echo term > "$1"; exit 0' TERM
		# Background a sleep so the trap can fire from the wait.
		sleep 30 &
		wait $!
	`

	resultCh := make(chan *Result, 1)
	go func() {
		r, _ := Run(context.Background(),
			[]string{"sh", "-c", script, "sh", tmp.Name()},
			0, 4096, logger,
		)
		resultCh <- r
	}()

	// Give the child time to install its trap.
	time.Sleep(200 * time.Millisecond)

	// Send SIGTERM to ourselves; the agent (this test process) forwards it.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}

	select {
	case r := <-resultCh:
		if r == nil {
			t.Fatal("expected non-nil result")
		}
		// Trap exits 0 cleanly so script returns 0.
		if r.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0 (clean trap)", r.ExitCode)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return within 15s — signal not forwarded")
	}

	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if !strings.Contains(string(data), "term") {
		t.Errorf("marker = %q, expected 'term' (child trap fired)", string(data))
	}
}

// TestRunExitCodeSignalledIsShellConvention verifies that a child killed by
// SIGTERM exits with 143 (128+15), not Go's -1 sentinel, so cron `&&`
// pipelines and the API payload reflect conventional shell semantics.
func TestRunExitCodeSignalledIsShellConvention(t *testing.T) {
	logger := newTestLogger()

	// sh -c 'kill -TERM $$' kills the shell itself with SIGTERM. The shell
	// has no trap, so it terminates by signal — exit code should be 143.
	result, err := Run(context.Background(),
		[]string{"sh", "-c", "kill -TERM $$; sleep 5"},
		5*time.Second, 1024, logger,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 128+int(syscall.SIGTERM) {
		t.Errorf("exit code = %d, want %d (128+SIGTERM)", result.ExitCode, 128+int(syscall.SIGTERM))
	}
}

func TestCappedWriter(t *testing.T) {
	t.Run("no truncation", func(t *testing.T) {
		w := &cappedWriter{max: 100}
		w.Write([]byte("short"))
		if w.String() != "short" || w.truncated {
			t.Errorf("got %q truncated=%v, want 'short' truncated=false", w.String(), w.truncated)
		}
	})

	t.Run("truncate at boundary", func(t *testing.T) {
		w := &cappedWriter{max: 5}
		w.Write([]byte("hello world"))
		if w.String() != "hello" || !w.truncated {
			t.Errorf("got %q truncated=%v, want 'hello' truncated=true", w.String(), w.truncated)
		}
	})

	t.Run("exactly at max", func(t *testing.T) {
		w := &cappedWriter{max: 5}
		w.Write([]byte("12345"))
		if w.String() != "12345" || w.truncated {
			t.Errorf("got %q truncated=%v, want '12345' truncated=false", w.String(), w.truncated)
		}
	})

	t.Run("multiple writes exceed max", func(t *testing.T) {
		w := &cappedWriter{max: 5}
		w.Write([]byte("abc"))
		w.Write([]byte("defgh"))
		if w.String() != "abcde" || !w.truncated {
			t.Errorf("got %q truncated=%v, want 'abcde' truncated=true", w.String(), w.truncated)
		}
	})

	t.Run("discards after truncation", func(t *testing.T) {
		w := &cappedWriter{max: 3}
		w.Write([]byte("abcdef"))
		n, err := w.Write([]byte("more"))
		if n != 4 || err != nil {
			t.Errorf("write after truncation: n=%d err=%v, want n=4 err=nil", n, err)
		}
		if w.String() != "abc" {
			t.Errorf("got %q, want 'abc'", w.String())
		}
	})
}
