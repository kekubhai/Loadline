// Command labd runs the INTENTIONALLY VULNERABLE OAST lab application.
//
// It is for local out-of-band callback demonstrations only. Both vulnerable
// behaviors are disabled unless explicitly enabled via environment variables.
// Never deploy this to a shared or public environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/lab"
)

const shutdownTimeout = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("labd", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := lab.LoadFromEnv(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("load lab configuration: %w", err)
	}

	server, err := lab.NewServer(cfg, logger)
	if err != nil {
		return fmt.Errorf("create lab server: %w", err)
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	logger.Warn("INTENTIONALLY VULNERABLE lab starting — local testing only",
		"addr", listener.Addr().String(),
		"callback_zone", cfg.CallbackZone,
		"http_callback_enabled", cfg.EnableHTTPCallback,
		"dns_callback_enabled", cfg.EnableDNSCallback,
		"oast_http_target", cfg.OASTHTTPTarget,
		"oast_dns_addr", cfg.OASTDNSAddr,
	)
	if !cfg.EnableHTTPCallback && !cfg.EnableDNSCallback {
		logger.Info("both demos are disabled; set LAB_ENABLE_HTTP_CALLBACK and/or LAB_ENABLE_DNS_CALLBACK to enable")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("lab http server: %w", err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("lab shutdown", "error", err)
	}
	logger.Info("labd stopped")
	return runErr
}
