package ping

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/platops-security/crond-agent/internal/config"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestConfig() *config.Config {
	return &config.Config{
		APIURL:         "",
		PingKey:        "test-key",
		Timeout:        0,
		MaxOutputBytes: 50 * 1024,
		LogLevel:       "info",
		LogFormat:      "text",
		Retries:        3,
		RetryBaseDelay: 1 * time.Millisecond, // Very short for fast tests
	}
}

func TestSendSuccess(t *testing.T) {
	var receivedReq *http.Request
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedReq = r
		// Read body before response
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.APIURL = server.URL
	logger := newTestLogger()

	client, err := NewClient(cfg, "1.0.0", logger)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	body := []byte(`{"output": "test"}`)

	err = client.Send(ctx, "test-key", "success", body)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedReq == nil {
		t.Fatal("no request received")
	}

	// Verify path
	expectedPath := "/ping/test-key"
	if receivedReq.URL.Path != expectedPath {
		t.Errorf("path = %s, want %s", receivedReq.URL.Path, expectedPath)
	}

	// Verify method
	if receivedReq.Method != http.MethodPost {
		t.Errorf("method = %s, want %s", receivedReq.Method, http.MethodPost)
	}

	// Verify headers
	if ua := receivedReq.Header.Get("User-Agent"); !strings.Contains(ua, "crond-agent/1.0.0") {
		t.Errorf("User-Agent = %s, should contain crond-agent/1.0.0", ua)
	}

	if ct := receivedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %s, want application/json", ct)
	}

	// Verify body
	if !bytes.Equal(receivedBody, body) {
		t.Errorf("body = %s, want %s", receivedBody, body)
	}
}

func TestSendRetryOn500(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			// First two requests return 500
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Third request succeeds
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.APIURL = server.URL
	cfg.Retries = 2 // Allow up to 3 attempts total (initial + 2 retries)
	cfg.RetryBaseDelay = 1 * time.Millisecond
	logger := newTestLogger()

	client, err := NewClient(cfg, "1.0.0", logger)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	err = client.Send(ctx, "test-key", "", nil)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if requestCount != 3 {
		t.Errorf("expected 3 requests, got %d", requestCount)
	}
}

func TestSendNoRetryOn400(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.APIURL = server.URL
	logger := newTestLogger()

	client, err := NewClient(cfg, "1.0.0", logger)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	err = client.Send(ctx, "test-key", "", nil)

	// Should get an error
	if err == nil {
		t.Fatal("expected error for 400 status, got nil")
	}

	// Should be exactly 1 request (no retries on 4xx)
	if requestCount != 1 {
		t.Errorf("expected 1 request, got %d", requestCount)
	}

	// Error should mention HTTP 400
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should contain '400', got %v", err)
	}
}

func TestSendRetryExhausted(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.APIURL = server.URL
	cfg.Retries = 2
	cfg.RetryBaseDelay = 1 * time.Millisecond
	logger := newTestLogger()

	client, err := NewClient(cfg, "1.0.0", logger)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	err = client.Send(ctx, "test-key", "", nil)

	// Should get an error after retries exhausted
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}

	// Should have attempted initial + retries (3 total)
	if requestCount != 3 {
		t.Errorf("expected 3 requests, got %d", requestCount)
	}

	// Error should mention HTTP 503
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should contain '503', got %v", err)
	}
}

func TestSendContextCancelled(t *testing.T) {
	blockChan := make(chan bool)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockChan // Block handler
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(blockChan)

	cfg := newTestConfig()
	cfg.APIURL = server.URL
	logger := newTestLogger()

	client, err := NewClient(cfg, "1.0.0", logger)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = client.Send(ctx, "test-key", "", nil)

	// Should get context error immediately (no retries)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}

	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error should contain context error, got %v", err)
	}
}

func TestSendWithKindParameter(t *testing.T) {
	tests := []struct {
		name         string
		kind         string
		expectedPath string
	}{
		{"empty kind", "", "/ping/test-key"},
		{"success kind", "success", "/ping/test-key"},
		{"start kind", "start", "/ping/test-key/start"},
		{"fail kind", "fail", "/ping/test-key/fail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := newTestConfig()
			cfg.APIURL = server.URL
			logger := newTestLogger()

			client, err := NewClient(cfg, "1.0.0", logger)
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}

			ctx := context.Background()
			err = client.Send(ctx, "test-key", tt.kind, nil)
			if err != nil {
				t.Fatalf("Send failed: %v", err)
			}

			if receivedPath != tt.expectedPath {
				t.Errorf("path = %s, want %s", receivedPath, tt.expectedPath)
			}
		})
	}
}

func TestNewClientWithInvalidCACert(t *testing.T) {
	cfg := newTestConfig()
	cfg.TLSCACert = "/nonexistent/ca.pem"
	logger := newTestLogger()

	_, err := NewClient(cfg, "1.0.0", logger)
	if err == nil {
		t.Fatal("expected error for nonexistent CA cert")
	}
}
