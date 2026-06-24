// Package ping sends HTTP pings to the crond.io API with retry and TLS support.
package ping

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/platops-security/crond-agent/internal/config"
)

// Client sends HTTP pings to the crond.io API with exponential backoff retries.
type Client struct {
	baseURL    string
	httpClient *http.Client
	version    string
	retries    int
	baseDelay  time.Duration
	logger     *slog.Logger
}

// NewClient constructs a Client with TLS config and retry settings from cfg.
func NewClient(cfg *config.Config, version string, logger *slog.Logger) (*Client, error) {
	tlsCfg, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("tls setup: %w", err)
	}

	transport := &http.Transport{}
	if tlsCfg != nil {
		transport.TLSClientConfig = tlsCfg
	}

	if cfg.TLSInsecureSkipVerify {
		// Print to plain stderr — slog output is often JSON and routed into
		// log aggregators where this signal is easy to overlook. We want
		// the operator who set this flag to see it on every run.
		fmt.Fprintln(os.Stderr, "WARNING: crond-agent TLS verification is disabled (tls_insecure_skip_verify=true) — credentials may be exposed to a man-in-the-middle. Do not use in production.")
		logger.Warn("TLS verification disabled — not recommended for production")
	}

	return &Client{
		baseURL:    cfg.APIURL,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
		version:    version,
		retries:    cfg.Retries,
		baseDelay:  cfg.RetryBaseDelay,
		logger:     logger,
	}, nil
}

// Send posts a ping for the given key. kind can be "", "start", "success", or "fail".
func (c *Client) Send(ctx context.Context, key, kind string, body []byte) error {
	url := fmt.Sprintf("%s/ping/%s", c.baseURL, key)
	if kind != "" && kind != "success" {
		url += "/" + kind
	}

	return c.sendWithRetry(ctx, url, body)
}

// sendWithRetry performs the HTTP POST with exponential backoff on retryable errors.
func (c *Client) sendWithRetry(ctx context.Context, url string, body []byte) error {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		// Build a fresh request + body reader each attempt.
		var bodyReader io.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bodyReader)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "crond-agent/"+c.version)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Check if context was cancelled — don't retry.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
		} else {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				if resp.StatusCode >= 400 {
					return fmt.Errorf("ping failed: HTTP %d", resp.StatusCode)
				}
				return nil // 2xx/3xx = success
			}
			lastErr = fmt.Errorf("ping failed: HTTP %d", resp.StatusCode)
		}

		// Don't sleep after the last attempt.
		if attempt < c.retries {
			// Cap the shift exponent so the exponential never overflows
			// time.Duration regardless of how high Retries is set. 20
			// already gives 2^20 * baseDelay which is far past the 30s cap.
			shift := attempt
			if shift > 20 {
				shift = 20
			}
			delay := c.baseDelay * (1 << shift)
			if delay > 30*time.Second {
				delay = 30 * time.Second // cap to prevent unbounded waits
			}
			c.logger.Warn("retrying ping", "attempt", attempt+1, "delay", delay, "error", lastErr)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	c.logger.Error("ping retries exhausted", "attempts", c.retries+1, "error", lastErr)
	return lastErr
}

// buildTLSConfig creates a *tls.Config from agent settings, or nil if no custom TLS needed.
func buildTLSConfig(cfg *config.Config) (*tls.Config, error) {
	if cfg.TLSCACert == "" && !cfg.TLSInsecureSkipVerify {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.TLSInsecureSkipVerify, //nolint:gosec // user-configured
	}

	if cfg.TLSCACert != "" {
		caCert, err := os.ReadFile(cfg.TLSCACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %s: %w", cfg.TLSCACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("invalid CA cert in %s", cfg.TLSCACert)
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}
