// Command crond-agent-injector is the Kubernetes mutating admission webhook
// server for crond-agent V2 auto-injection. It is a SEPARATE binary/image from
// the agent so the agent stays tiny (it is copied into every wrapped job pod);
// the injector may pull in a larger runtime without affecting that hot path.
//
// It serves HTTPS on /mutate (AdmissionReview) plus /healthz and /readyz.
// TLS cert/key and the injection template config are mounted by the Helm chart.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/platops-security/crond-agent/internal/webhook"
)

// Injected at build time via -ldflags (matches the agent binary).
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	addr := flag.String("listen", ":8443", "HTTPS listen address")
	certFile := flag.String("tls-cert", "/tls/tls.crt", "path to the server certificate")
	keyFile := flag.String("tls-key", "/tls/tls.key", "path to the server private key")
	configPath := flag.String("config", "/config/injector.yaml", "path to the injection template config")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := webhook.LoadConfig(*configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid config", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/mutate", webhook.NewHandler(cfg, logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	// Graceful shutdown on SIGTERM/SIGINT so in-flight admissions finish.
	idleClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown", "error", err)
		}
		close(idleClosed)
	}()

	logger.Info("crond-agent injector listening",
		"addr", *addr, "version", Version, "commit", Commit, "built", BuildDate,
		"apiUrl", cfg.APIURL, "agentImage", cfg.AgentImage)

	if err := srv.ListenAndServeTLS(*certFile, *keyFile); err != nil && err != http.ErrServerClosed {
		logger.Error("server", "error", err)
		os.Exit(1)
	}
	<-idleClosed
}
