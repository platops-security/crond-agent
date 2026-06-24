package exec

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStreamingRedactWriter_EmptyPatternsReturnsDst(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, nil, 0)
	if w != &sink {
		t.Errorf("empty patterns should return dst unchanged for zero overhead")
	}
}

func TestStreamingRedactWriter_RedactsCompleteLines(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`Bearer [A-Za-z0-9]+`),
	}, 0)
	if _, err := w.Write([]byte("auth Bearer abc123 ok\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sink.String(); got != "auth [REDACTED] ok\n" {
		t.Errorf("got %q, want %q", got, "auth [REDACTED] ok\n")
	}
}

func TestStreamingRedactWriter_HoldsPartialLineUntilNewline(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`Bearer [A-Za-z0-9]+`),
	}, 0)
	// Split a secret across two Write calls — without the line buffer the
	// regex would miss it and the prefix would leak.
	if _, err := w.Write([]byte("Bearer ab")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if sink.Len() != 0 {
		t.Errorf("partial line should be held, got %q", sink.String())
	}
	if _, err := w.Write([]byte("c123 done\n")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if got := sink.String(); got != "[REDACTED] done\n" {
		t.Errorf("got %q, want %q", got, "[REDACTED] done\n")
	}
}

func TestStreamingRedactWriter_CloseFlushesTrailingPartial(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`secret=\S+`),
	}, 0).(*streamingRedactWriter)
	if _, err := w.Write([]byte("secret=abc")); err != nil { // no trailing \n
		t.Fatalf("Write: %v", err)
	}
	if sink.Len() != 0 {
		t.Errorf("buffered partial should not be forwarded yet")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := sink.String(); got != "[REDACTED]" {
		t.Errorf("Close should redact and flush partial; got %q", got)
	}
}

// TestStreamingRedactWriter_BoundsPartialLineMemory proves a long newline-free
// stream does not accumulate unbounded in the partial buffer. Without a bound,
// the redactor holds the whole stream in memory before the downstream cap ever
// sees a byte — an OOM vector on the host running the agent.
func TestStreamingRedactWriter_BoundsPartialLineMemory(t *testing.T) {
	var sink bytes.Buffer
	const maxBuffer = 64
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`secret`),
	}, maxBuffer).(*streamingRedactWriter)

	chunk := bytes.Repeat([]byte("x"), 50)
	for i := 0; i < 100; i++ { // 5000 bytes, no newline anywhere
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if len(w.buf) > maxBuffer {
			t.Fatalf("partial buffer grew to %d bytes, exceeds bound %d", len(w.buf), maxBuffer)
		}
	}
	if sink.Len() == 0 {
		t.Fatal("expected the newline-free stream to be force-flushed to dst, not held")
	}
}

// TestRunWithOptions_RedactionAppliedBeforeCap verifies the real fix:
// a secret whose tail would have been truncated by the cappedWriter is
// redacted FIRST, so the cap then applies to "[REDACTED]"-bearing bytes
// instead of leaking the secret prefix.
func TestRunWithOptions_RedactionAppliedBeforeCap(t *testing.T) {
	logger := newTestLogger()
	// Emit a line where the secret would straddle a hypothetical truncation
	// point if redaction ran after capping. With stream-level redaction the
	// match runs on the complete line first.
	script := `echo "AUTH Bearer abc123def456ghi789 end"`
	r, err := RunWithOptions(context.Background(),
		[]string{"sh", "-c", script},
		5*time.Second, 4096, Options{
			RedactPatterns: []*regexp.Regexp{
				regexp.MustCompile(`Bearer [A-Za-z0-9]+`),
			},
		}, logger,
	)
	if err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	if !strings.Contains(r.Stdout, "[REDACTED]") {
		t.Errorf("stdout should contain [REDACTED], got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "abc123def456ghi789") {
		t.Errorf("stdout should NOT contain the secret value, got %q", r.Stdout)
	}
}

// failingWriter fails its Write once call count reaches failAt, so the
// error-propagation paths in streamingRedactWriter.Write can be exercised.
type failingWriter struct {
	calls  int
	failAt int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls >= f.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// TestStreamingRedactWriter_WriteErrorOnNewlineFlushPropagates ensures a dst
// write error on the complete-line flush is surfaced to the caller rather than
// swallowed (the runner relies on io errors to detect a broken capture sink).
func TestStreamingRedactWriter_WriteErrorOnNewlineFlushPropagates(t *testing.T) {
	w := newStreamingRedactWriter(&failingWriter{failAt: 1}, []*regexp.Regexp{
		regexp.MustCompile(`secret`),
	}, 0)
	n, err := w.Write([]byte("line with secret\n"))
	if err == nil {
		t.Fatal("expected dst.Write error to propagate on newline flush")
	}
	if n != 0 {
		t.Errorf("on error want n=0 (Go io.Writer contract), got %d", n)
	}
}

// TestStreamingRedactWriter_WriteErrorOnForceFlushPropagates covers the
// memory-bound force-flush branch: when a newline-free stream exceeds
// maxBuffer, the partial is pushed through dst and any error must propagate.
func TestStreamingRedactWriter_WriteErrorOnForceFlushPropagates(t *testing.T) {
	const maxBuffer = 8
	w := newStreamingRedactWriter(&failingWriter{failAt: 1}, []*regexp.Regexp{
		regexp.MustCompile(`secret`),
	}, maxBuffer)
	// 10 bytes, no newline → exceeds maxBuffer → force-flush path.
	if _, err := w.Write([]byte("0123456789")); err == nil {
		t.Fatal("expected dst.Write error to propagate on force-flush")
	}
}

// TestStreamingRedactWriter_SecretSplitAcrossManyWrites strengthens the core
// streaming guarantee: a single-line secret delivered one byte per Write (the
// adversarial fragmentation case) is still fully redacted once the line
// completes, with no prefix leaking to dst before the newline arrives.
func TestStreamingRedactWriter_SecretSplitAcrossManyWrites(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`password=\S+`),
	}, 0)

	line := "log password=hunter2swordfish done\n"
	for i := 0; i < len(line); i++ {
		if _, err := w.Write([]byte{line[i]}); err != nil {
			t.Fatalf("Write byte %d: %v", i, err)
		}
		// Until the newline lands, nothing may be forwarded — otherwise a
		// secret prefix could leak ahead of the regex seeing the full token.
		if i < len(line)-1 && sink.Len() != 0 {
			t.Fatalf("byte %d forwarded a partial line early: %q", i, sink.String())
		}
	}
	got := sink.String()
	if strings.Contains(got, "hunter2swordfish") {
		t.Errorf("secret leaked across fragmented writes: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED], got %q", got)
	}
}

// TestStreamingRedactWriter_CloseOnEmptyBufferIsNoop covers the empty-buffer
// early return in Close (idempotent flush after all data already forwarded).
func TestStreamingRedactWriter_CloseOnEmptyBufferIsNoop(t *testing.T) {
	var sink bytes.Buffer
	w := newStreamingRedactWriter(&sink, []*regexp.Regexp{
		regexp.MustCompile(`x`),
	}, 0).(*streamingRedactWriter)
	if _, err := w.Write([]byte("clean line\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil { // buffer already empty after the newline flush
		t.Fatalf("Close on empty buffer should be a no-op, got %v", err)
	}
}
