// Command oastd runs the OAST engine: an HTTP callback receiver, an
// authoritative DNS callback receiver, and optional MCP transports backed by
// the same in-memory registry.
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

	"github.com/miekg/dns"

	"github.com/kekubhai/Loadline/apps/oast/internal/callback"
	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/config"
	"github.com/kekubhai/Loadline/apps/oast/internal/mcp"
	"github.com/kekubhai/Loadline/apps/oast/internal/ratelimit"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("oastd", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	clk := clock.System{}
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          cfg.MaxActiveTests,
		MaxTestLifetime:         cfg.MaxTestLifetime,
		MaxEvidencePerTest:      cfg.MaxEvidencePerTest,
		MaxEvidenceBytesPerTest: cfg.MaxEvidenceBytesPerTest,
	}, clk, token.CryptoGenerator{})
	if err != nil {
		return fmt.Errorf("create registry: %w", err)
	}

	scopes, err := scope.New(cfg.AuthorizedScopes)
	if err != nil {
		return fmt.Errorf("create scope validator: %w", err)
	}
	svc, err := service.New(store, scopes, service.Config{
		DefaultTTL: cfg.DefaultTestTTL,
		MaxTTL:     cfg.MaxTestLifetime,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}

	httpHandler, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain:   cfg.PublicDomain,
		MaxBodyBytes:   cfg.MaxBodyBytes,
		MaxHeaderBytes: cfg.MaxHeaderBytes,
		MaxQueryBytes:  cfg.MaxQueryBytes,
	}, store, ratelimit.New(cfg.RateLimitPerSecond, cfg.RateLimitBurst, clk), logger)
	if err != nil {
		return err
	}
	dnsHandler, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: cfg.PublicDomain,
		IPv4: cfg.CallbackIPv4,
		IPv6: cfg.CallbackIPv6,
	}, store, ratelimit.New(cfg.RateLimitPerSecond, cfg.RateLimitBurst, clk), logger)
	if err != nil {
		return err
	}

	httpListener, err := net.Listen("tcp", cfg.HTTPListenAddr())
	if err != nil {
		return fmt.Errorf("listen http %s: %w", cfg.HTTPListenAddr(), err)
	}
	udpConn, tcpListener, err := listenDNS(cfg.DNSListenAddr())
	if err != nil {
		_ = httpListener.Close()
		return fmt.Errorf("listen dns %s: %w", cfg.DNSListenAddr(), err)
	}
	closeListeners := func() {
		_ = httpListener.Close()
		_ = udpConn.Close()
		_ = tcpListener.Close()
	}

	callbackAddr, ok := httpListener.Addr().(*net.TCPAddr)
	if !ok {
		closeListeners()
		return fmt.Errorf("unexpected HTTP listener address %T", httpListener.Addr())
	}

	var mcpServer *mcp.Server
	var mcpListener net.Listener
	var mcpHTTPServer *http.Server
	if cfg.MCPTransport != config.MCPTransportOff {
		mcpServer, err = mcp.New(mcp.Config{
			Service:      svc,
			PublicDomain: cfg.PublicDomain,
			HTTPPort:     callbackAddr.Port,
			Logger:       logger,
		})
		if err != nil {
			closeListeners()
			return fmt.Errorf("create MCP server: %w", err)
		}
	}
	if cfg.MCPTransport == config.MCPTransportHTTP || cfg.MCPTransport == config.MCPTransportBoth {
		mcpListener, err = net.Listen("tcp", cfg.MCPHTTPListenAddr())
		if err != nil {
			closeListeners()
			return fmt.Errorf("listen MCP HTTP %s: %w", cfg.MCPHTTPListenAddr(), err)
		}
		mux := http.NewServeMux()
		mux.Handle(cfg.MCPHTTPPath, mcpServer.StreamableHTTPHandler(cfg.MaxBodyBytes))
		mcpHTTPServer = &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      45 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    cfg.MaxHeaderBytes,
		}
	}

	httpServer := &http.Server{
		Handler:           httpHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
	udpServer := &dns.Server{PacketConn: udpConn, Handler: dnsHandler}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: dnsHandler}

	logger.Info("oastd starting",
		"http", httpListener.Addr().String(),
		"dns_udp", udpConn.LocalAddr().String(),
		"dns_tcp", tcpListener.Addr().String(),
		"domain", cfg.PublicDomain,
		"authorized_scopes", len(cfg.AuthorizedScopes),
		"max_active_tests", cfg.MaxActiveTests,
		"max_test_lifetime", cfg.MaxTestLifetime.String(),
		"mcp_transport", cfg.MCPTransport,
	)
	if mcpListener != nil {
		logger.Info("MCP HTTP listening", "addr", mcpListener.Addr().String(), "path", cfg.MCPHTTPPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 5)
	go func() {
		if err := httpServer.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		if err := udpServer.ActivateAndServe(); err != nil && !isClosedError(err) {
			errCh <- fmt.Errorf("dns udp server: %w", err)
		}
	}()
	go func() {
		if err := tcpServer.ActivateAndServe(); err != nil && !isClosedError(err) {
			errCh <- fmt.Errorf("dns tcp server: %w", err)
		}
	}()
	if mcpHTTPServer != nil {
		go func() {
			if err := mcpHTTPServer.Serve(mcpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("mcp http server: %w", err)
			}
		}()
	}

	var stdioDone <-chan struct{}
	if cfg.MCPTransport == config.MCPTransportStdio || cfg.MCPTransport == config.MCPTransportBoth {
		done := make(chan struct{})
		stdioDone = done
		go func() {
			if err := mcpServer.RunStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("mcp stdio: %w", err)
				return
			}
			close(done)
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	case <-stdioDone:
		logger.Info("MCP stdio closed")
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "error", err)
	}
	if mcpHTTPServer != nil {
		if err := mcpHTTPServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("mcp http shutdown", "error", err)
		}
	}
	if err := udpServer.ShutdownContext(shutdownCtx); err != nil {
		logger.Warn("dns udp shutdown", "error", err)
	}
	if err := tcpServer.ShutdownContext(shutdownCtx); err != nil {
		logger.Warn("dns tcp shutdown", "error", err)
	}
	logger.Info("oastd stopped")
	return runErr
}

func listenDNS(addr string) (net.PacketConn, net.Listener, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	host, port, err := net.SplitHostPort(pc.LocalAddr().String())
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	return pc, ln, nil
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	// miekg/dns does not export a sentinel for this case.
	return err.Error() == "server shutdown" || err.Error() == "dns: server shutdown"
}
