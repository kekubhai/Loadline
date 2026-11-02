// Package config loads and validates the OAST service configuration from
// environment variables. Loading fails closed: missing authorized scopes,
// unusable callback addresses, or unsafe limits stop the process at boot.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
)

// Config is the validated runtime configuration.
type Config struct {
	HTTPAddr string
	HTTPPort int

	DNSAddr string
	DNSPort int

	PublicDomain string
	CallbackIPv4 net.IP
	CallbackIPv6 net.IP

	AuthorizedScopes []string

	MaxTestLifetime time.Duration
	DefaultTestTTL  time.Duration
	MaxActiveTests  int

	MaxBodyBytes            int64
	MaxHeaderBytes          int
	MaxQueryBytes           int
	MaxEvidencePerTest      int
	MaxEvidenceBytesPerTest int64

	RateLimitPerSecond float64
	RateLimitBurst     int

	MCPTransport string
	MCPHTTPAddr  string
	MCPHTTPPort  int
	MCPHTTPPath  string
}

const (
	defaultHTTPAddr    = "127.0.0.1"
	defaultHTTPPort    = 8081
	defaultDNSAddr     = "127.0.0.1"
	defaultDNSPort     = 5353
	defaultDomain      = "oast.local"
	defaultMCPHTTPAddr = "127.0.0.1"
	defaultMCPHTTPPort = 8082
	defaultMCPHTTPPath = "/mcp"

	MCPTransportOff   = "off"
	MCPTransportStdio = "stdio"
	MCPTransportHTTP  = "http"
	MCPTransportBoth  = "both"
)

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return LoadFromEnv(os.LookupEnv)
}

// LoadFromEnv loads configuration through lookup. It is exported for tests
// and for embedders that use a non-process environment.
func LoadFromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		HTTPAddr:                raw(lookup, "OAST_HTTP_ADDR", defaultHTTPAddr),
		HTTPPort:                defaultHTTPPort,
		DNSAddr:                 raw(lookup, "OAST_DNS_ADDR", defaultDNSAddr),
		DNSPort:                 defaultDNSPort,
		PublicDomain:            defaultDomain,
		MaxTestLifetime:         time.Hour,
		DefaultTestTTL:          15 * time.Minute,
		MaxActiveTests:          100,
		MaxBodyBytes:            64 * 1024,
		MaxHeaderBytes:          16 * 1024,
		MaxQueryBytes:           4 * 1024,
		MaxEvidencePerTest:      500,
		MaxEvidenceBytesPerTest: 1024 * 1024,
		RateLimitPerSecond:      100,
		RateLimitBurst:          200,
		MCPTransport:            MCPTransportOff,
		MCPHTTPAddr:             defaultMCPHTTPAddr,
		MCPHTTPPort:             defaultMCPHTTPPort,
		MCPHTTPPath:             defaultMCPHTTPPath,
	}

	var err error
	if cfg.HTTPPort, err = intEnv(lookup, "OAST_HTTP_PORT", defaultHTTPPort, 0, 65535); err != nil {
		return Config{}, err
	}
	if cfg.DNSPort, err = intEnv(lookup, "OAST_DNS_PORT", defaultDNSPort, 0, 65535); err != nil {
		return Config{}, err
	}
	if err := validateListenHost("OAST_HTTP_ADDR", cfg.HTTPAddr); err != nil {
		return Config{}, err
	}
	if err := validateListenHost("OAST_DNS_ADDR", cfg.DNSAddr); err != nil {
		return Config{}, err
	}

	cfg.PublicDomain, err = loadDomain(lookup, "OAST_PUBLIC_DOMAIN", defaultDomain)
	if err != nil {
		return Config{}, err
	}

	if cfg.CallbackIPv4, err = loadIP(lookup, "OAST_CALLBACK_IPV4", "127.0.0.1", 4); err != nil {
		return Config{}, err
	}
	if cfg.CallbackIPv6, err = loadIP(lookup, "OAST_CALLBACK_IPV6", "::1", 6); err != nil {
		return Config{}, err
	}
	if cfg.CallbackIPv4 == nil && cfg.CallbackIPv6 == nil {
		return Config{}, fmt.Errorf("at least one of OAST_CALLBACK_IPV4 or OAST_CALLBACK_IPV6 must be configured")
	}

	scopesRaw, ok := lookup("OAST_AUTHORIZED_SCOPES")
	if !ok || strings.TrimSpace(scopesRaw) == "" {
		return Config{}, fmt.Errorf("OAST_AUTHORIZED_SCOPES is required and must contain at least one scope")
	}
	for _, part := range strings.Split(scopesRaw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			cfg.AuthorizedScopes = append(cfg.AuthorizedScopes, part)
		}
	}
	if _, err := scope.New(cfg.AuthorizedScopes); err != nil {
		return Config{}, fmt.Errorf("OAST_AUTHORIZED_SCOPES: %w", err)
	}

	if cfg.MaxTestLifetime, err = durationEnv(lookup, "OAST_MAX_TEST_LIFETIME", time.Hour, time.Minute, 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.DefaultTestTTL, err = durationEnv(lookup, "OAST_DEFAULT_TEST_TTL", 15*time.Minute, time.Minute, cfg.MaxTestLifetime); err != nil {
		return Config{}, err
	}
	if cfg.MaxActiveTests, err = intEnv(lookup, "OAST_MAX_ACTIVE_TESTS", 100, 1, 10000); err != nil {
		return Config{}, err
	}
	if cfg.MaxBodyBytes, err = int64Env(lookup, "OAST_MAX_BODY_BYTES", 64*1024, 1, 10*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.MaxHeaderBytes, err = intEnv(lookup, "OAST_MAX_HEADER_BYTES", 16*1024, 1024, 1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.MaxQueryBytes, err = intEnv(lookup, "OAST_MAX_QUERY_BYTES", 4*1024, 128, 64*1024); err != nil {
		return Config{}, err
	}
	if cfg.MaxEvidencePerTest, err = intEnv(lookup, "OAST_MAX_EVIDENCE_PER_TEST", 500, 1, 10000); err != nil {
		return Config{}, err
	}
	if cfg.MaxEvidenceBytesPerTest, err = int64Env(lookup, "OAST_MAX_EVIDENCE_BYTES_PER_TEST", 1024*1024, 1024, 64*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitPerSecond, err = floatEnv(lookup, "OAST_RATE_LIMIT_PER_SECOND", 100, 0.001, 1_000_000); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitBurst, err = intEnv(lookup, "OAST_RATE_LIMIT_BURST", 200, 1, 1_000_000); err != nil {
		return Config{}, err
	}

	cfg.MCPTransport = strings.ToLower(raw(lookup, "OAST_MCP_TRANSPORT", MCPTransportOff))
	switch cfg.MCPTransport {
	case MCPTransportOff, MCPTransportStdio, MCPTransportHTTP, MCPTransportBoth:
	default:
		return Config{}, fmt.Errorf("OAST_MCP_TRANSPORT must be one of off, stdio, http, or both")
	}
	cfg.MCPHTTPAddr = raw(lookup, "OAST_MCP_HTTP_ADDR", defaultMCPHTTPAddr)
	if err := validateMCPListenHost("OAST_MCP_HTTP_ADDR", cfg.MCPHTTPAddr); err != nil {
		return Config{}, err
	}
	if cfg.MCPHTTPPort, err = intEnv(lookup, "OAST_MCP_HTTP_PORT", defaultMCPHTTPPort, 1, 65535); err != nil {
		return Config{}, err
	}
	cfg.MCPHTTPPath = raw(lookup, "OAST_MCP_HTTP_PATH", defaultMCPHTTPPath)
	if !strings.HasPrefix(cfg.MCPHTTPPath, "/") || strings.ContainsAny(cfg.MCPHTTPPath, "?# \t\r\n") {
		return Config{}, fmt.Errorf("OAST_MCP_HTTP_PATH must be an absolute path without a query, fragment, or whitespace")
	}

	return cfg, nil
}

// HTTPListenAddr returns the HTTP listener address in host:port form.
func (c Config) HTTPListenAddr() string {
	return net.JoinHostPort(c.HTTPAddr, strconv.Itoa(c.HTTPPort))
}

// DNSListenAddr returns the DNS listener address in host:port form.
func (c Config) DNSListenAddr() string {
	return net.JoinHostPort(c.DNSAddr, strconv.Itoa(c.DNSPort))
}

// MCPHTTPListenAddr returns the optional MCP HTTP listener address.
func (c Config) MCPHTTPListenAddr() string {
	return net.JoinHostPort(c.MCPHTTPAddr, strconv.Itoa(c.MCPHTTPPort))
}

func raw(lookup func(string) (string, bool), key, def string) string {
	if v, ok := lookup(key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func intEnv(lookup func(string) (string, bool), key string, def, min, max int) (int, error) {
	v := raw(lookup, key, strconv.Itoa(def))
	if v == "" {
		return 0, fmt.Errorf("%s cannot be empty", key)
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
	}
	return n, nil
}

func int64Env(lookup func(string) (string, bool), key string, def, min, max int64) (int64, error) {
	v := raw(lookup, key, strconv.FormatInt(def, 10))
	if v == "" {
		return 0, fmt.Errorf("%s cannot be empty", key)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
	}
	return n, nil
}

func floatEnv(lookup func(string) (string, bool), key string, def, min, max float64) (float64, error) {
	v := raw(lookup, key, strconv.FormatFloat(def, 'f', -1, 64))
	if v == "" {
		return 0, fmt.Errorf("%s cannot be empty", key)
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be a number between %g and %g", key, min, max)
	}
	return n, nil
}

func durationEnv(lookup func(string) (string, bool), key string, def, min, max time.Duration) (time.Duration, error) {
	v := raw(lookup, key, def.String())
	d, err := time.ParseDuration(v)
	if err != nil || d < min || d > max {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", key, min, max)
	}
	return d, nil
}

func loadDomain(lookup func(string) (string, bool), key, def string) (string, error) {
	v := raw(lookup, key, def)
	host, err := scope.NormalizeHostname(v)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("%s must be a DNS name, not an IP address", key)
	}
	return host, nil
}

func loadIP(lookup func(string) (string, bool), key, def string, family int) (net.IP, error) {
	v := raw(lookup, key, def)
	if v == "" {
		return nil, nil
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return nil, fmt.Errorf("%s is not a valid IP address", key)
	}
	if family == 4 {
		ip4 := ip.To4()
		if ip4 == nil {
			return nil, fmt.Errorf("%s must be an IPv4 address", key)
		}
		return ip4, nil
	}
	if ip.To4() != nil {
		return nil, fmt.Errorf("%s must be an IPv6 address", key)
	}
	if ip.To16() == nil {
		return nil, fmt.Errorf("%s is not a valid IPv6 address", key)
	}
	return ip, nil
}

func validateListenHost(key, host string) error {
	if host == "" {
		return fmt.Errorf("%s cannot be empty", key)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if _, err := scope.NormalizeHostname(host); err != nil {
		return fmt.Errorf("%s is not a usable listen host: %w", key, err)
	}
	return nil
}

func validateMCPListenHost(key, host string) error {
	if host == "" {
		return fmt.Errorf("%s cannot be empty", key)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("%s must be a loopback address because the MCP endpoint is unauthenticated", key)
		}
		return nil
	}
	normalized, err := scope.NormalizeHostname(host)
	if err != nil || normalized != "localhost" {
		return fmt.Errorf("%s must be a loopback address or localhost", key)
	}
	return nil
}
