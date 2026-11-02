// Package mcp exposes the OAST service through the Model Context Protocol.
// It is a transport adapter: authorization, lifecycle, and evidence behavior
// remain in the existing service and registry packages.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
)

const (
	serverName    = "loadline-oast"
	serverVersion = "0.2.0"
)

// Config controls the MCP adapter. Callback URLs use the same public domain
// and HTTP port as the callback receiver.
type Config struct {
	Service      *service.Service
	PublicDomain string
	HTTPPort     int
	Logger       *slog.Logger
}

// Server owns the MCP server and its engine dependencies.
type Server struct {
	sdk          *sdk.Server
	service      *service.Service
	publicDomain string
	httpPort     int
	logger       *slog.Logger
}

// New validates the adapter configuration and registers the OAST tools.
func New(cfg Config) (*Server, error) {
	if cfg.Service == nil {
		return nil, errors.New("mcp: service is required")
	}
	domain, err := scope.NormalizeHostname(cfg.PublicDomain)
	if err != nil {
		return nil, fmt.Errorf("mcp: invalid public domain: %w", err)
	}
	if net.ParseIP(domain) != nil {
		return nil, errors.New("mcp: public domain must be a DNS name, not an IP address")
	}
	if cfg.HTTPPort < 1 || cfg.HTTPPort > 65535 {
		return nil, fmt.Errorf("mcp: HTTP port must be between 1 and 65535")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	s := &Server{
		service:      cfg.Service,
		publicDomain: domain,
		httpPort:     cfg.HTTPPort,
		logger:       logger,
	}
	s.sdk = sdk.NewServer(&sdk.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &sdk.ServerOptions{
		Instructions: "Create only authorized OAST tests. Callbacks are observed interactions, not proof of exploitation.",
		Logger:       logger,
	})
	s.registerTools()
	return s, nil
}

// SDK returns the underlying SDK server for transport setup and tests.
func (s *Server) SDK() *sdk.Server { return s.sdk }

// Run serves MCP over a transport until the client disconnects or ctx ends.
func (s *Server) Run(ctx context.Context, transport sdk.Transport) error {
	return s.sdk.Run(ctx, transport)
}

// RunStdio serves MCP over stdin/stdout.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}

// StreamableHTTPHandler returns a stateless Streamable HTTP handler.
func (s *Server) StreamableHTTPHandler(maxRequestBodyBytes int64) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return s.sdk
	}, &sdk.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		Logger:              s.logger,
		MaxRequestBodyBytes: maxRequestBodyBytes,
	})
}

// Connect starts an MCP session over transport.
func (s *Server) Connect(ctx context.Context, transport sdk.Transport) (*sdk.ServerSession, error) {
	return s.sdk.Connect(ctx, transport, nil)
}

func (s *Server) callbackURL(token string) string {
	host := token + "." + s.publicDomain
	if s.httpPort == 80 {
		return "http://" + host + "/"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(s.httpPort)) + "/"
}

func (s *Server) dnsHostname(token string) string {
	return token + "." + s.publicDomain
}

type toolError struct {
	Code    string `json:"code" jsonschema:"stable machine-readable error code"`
	Message string `json:"message" jsonschema:"safe human-readable error message"`
}

type envelope[T any] struct {
	Data  *T         `json:"data,omitempty" jsonschema:"tool result when the call succeeds"`
	Error *toolError `json:"error,omitempty" jsonschema:"tool error when the call fails"`
}

func success[T any](value T) (*sdk.CallToolResult, envelope[T], error) {
	return nil, envelope[T]{Data: &value}, nil
}

func failure[T any](err error) (*sdk.CallToolResult, envelope[T], error) {
	return &sdk.CallToolResult{IsError: true}, envelope[T]{Error: toolErrorFrom(err)}, nil
}

func failureWithData[T any](value T, err error) (*sdk.CallToolResult, envelope[T], error) {
	return &sdk.CallToolResult{IsError: true}, envelope[T]{
		Data:  &value,
		Error: toolErrorFrom(err),
	}, nil
}

var errInvalidInput = errors.New("invalid input")

// logToolError records a rejected or failed tool call. It logs only the tool
// name and the safe, mapped error code — never the target, token, or raw
// error — so logs cannot leak scope targets or secrets. Out-of-scope
// rejections (code "unauthorized") and all other rejections are visible to an
// operator; genuine internal failures are logged at error level.
func (s *Server) logToolError(tool string, te *toolError) {
	if te == nil {
		return
	}
	if te.Code == "internal" {
		s.logger.Error("mcp tool failed", "tool", tool, "code", te.Code)
		return
	}
	s.logger.Warn("mcp tool rejected", "tool", tool, "code", te.Code)
}

func toolErrorFrom(err error) *toolError {
	code := "internal"
	message := "internal error"
	switch {
	case errors.Is(err, errInvalidInput):
		code, message = "invalid_input", err.Error()
	case errors.Is(err, service.ErrTargetRequired),
		errors.Is(err, service.ErrAuthorizationRequired),
		errors.Is(err, service.ErrInvalidAuthorization),
		errors.Is(err, service.ErrInvalidListOptions),
		errors.Is(err, registry.ErrInvalidTTL),
		errors.Is(err, registry.ErrTTLTooLong),
		errors.Is(err, registry.ErrInvalidRequest),
		errors.Is(err, registry.ErrInvalidEvidence):
		code, message = "invalid_input", err.Error()
	case errors.Is(err, scope.ErrInvalidTarget):
		code, message = "invalid_input", err.Error()
	case errors.Is(err, scope.ErrOutOfScope):
		code, message = "unauthorized", err.Error()
	case errors.Is(err, registry.ErrNotFound):
		code, message = "test_not_found", "test not found"
	case errors.Is(err, registry.ErrClosed):
		code, message = "test_closed", "test is closed"
	case errors.Is(err, registry.ErrExpired):
		code, message = "test_expired", "test is expired"
	case errors.Is(err, registry.ErrActiveLimit), errors.Is(err, registry.ErrEvidenceLimit):
		code, message = "limit_exceeded", err.Error()
	case errors.Is(err, context.Canceled):
		code, message = "canceled", "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "timeout", "request timed out"
	default:
		code, message = "internal", "internal error"
	}
	return &toolError{Code: code, Message: message}
}
