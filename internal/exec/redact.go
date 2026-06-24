package exec

import (
	"bytes"
	"io"
	"regexp"
)

// redactPlaceholder is the string substituted for every pattern match.
var redactPlaceholder = []byte("[REDACTED]")

// streamingRedactWriter wraps an io.Writer with line-buffered regex
// replacement. Complete lines are redacted and forwarded immediately;
// the trailing partial line is held until either a newline arrives or
// Close is called.
//
// Line-buffering is the right tradeoff for cron output: secrets nearly
// always live on a single line ("DSN=postgres://...", "Bearer xyz",
// "password=…"), and the alternative — buffering the entire stream — would
// defeat the OOM guard cappedWriter provides downstream. Multi-line
// secrets (PEM blocks etc.) fall outside this model; operators with such
// payloads should set capture_output: false.
type streamingRedactWriter struct {
	dst       io.Writer
	patterns  []*regexp.Regexp
	buf       []byte
	maxBuffer int // force-flush the partial line once it reaches this size; 0 = unbounded
}

// newStreamingRedactWriter returns a writer that applies patterns to
// complete lines before forwarding to dst. If patterns is empty the
// returned writer is dst itself (no allocation, no overhead).
//
// maxBuffer bounds the held partial line so an unbounded newline-free stream
// cannot grow memory without limit and defeat the downstream cap. Pass 0 to
// disable the bound (tests that exercise pure line-buffering); production wires
// it to max_output_bytes.
func newStreamingRedactWriter(dst io.Writer, patterns []*regexp.Regexp, maxBuffer int) io.Writer {
	if len(patterns) == 0 {
		return dst
	}
	return &streamingRedactWriter{dst: dst, patterns: patterns, maxBuffer: maxBuffer}
}

func (r *streamingRedactWriter) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	if last := bytes.LastIndexByte(r.buf, '\n'); last >= 0 {
		if _, err := r.dst.Write(r.redact(r.buf[:last+1])); err != nil {
			return 0, err
		}
		// Preserve the trailing partial for the next call.
		r.buf = append(r.buf[:0], r.buf[last+1:]...)
	}
	// Bound the held partial line. A newline-free stream would otherwise grow
	// r.buf without limit and defeat the downstream cap (OOM on the host). Past
	// the threshold, force the partial through the redactor so memory stays
	// bounded. A secret straddling this forced boundary may have its head
	// emitted before the regex sees the whole line — the same "single line
	// longer than max_output_bytes" caveat documented in the README threat
	// model; raising max_output_bytes or capture_output:false is the mitigation.
	if r.maxBuffer > 0 && len(r.buf) >= r.maxBuffer {
		if _, err := r.dst.Write(r.redact(r.buf)); err != nil {
			return 0, err
		}
		r.buf = r.buf[:0]
	}
	// Report the full length so io.MultiWriter doesn't treat this as a short write.
	return len(p), nil
}

// Close flushes any held partial line. Must be called once after the
// last Write to avoid losing data that didn't end in '\n'.
func (r *streamingRedactWriter) Close() error {
	if len(r.buf) == 0 {
		return nil
	}
	_, err := r.dst.Write(r.redact(r.buf))
	r.buf = nil
	return err
}

func (r *streamingRedactWriter) redact(b []byte) []byte {
	for _, re := range r.patterns {
		b = re.ReplaceAll(b, redactPlaceholder)
	}
	return b
}
