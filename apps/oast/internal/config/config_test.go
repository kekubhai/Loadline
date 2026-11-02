package config

import (
	"strings"
	"testing"
	"time"
)

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromEnv(envLookup(map[string]string{
		"OAST_AUTHORIZED_SCOPES": "lab.local, 127.0.0.1",
	}))
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1" || cfg.HTTPPort != 8081 {
		t.Fatalf("HTTP defaults = %s:%d", cfg.HTTPAddr, cfg.HTTPPort)
	}
	if cfg.DNSAddr != "127.0.0.1" || cfg.DNSPort != 5353 {
		t.Fatalf("DNS defaults = %s:%d", cfg.DNSAddr, cfg.DNSPort)
	}
	if cfg.PublicDomain != "oast.local" {
		t.Fatalf("PublicDomain = %q", cfg.PublicDomain)
	}
	if cfg.CallbackIPv4 == nil || cfg.CallbackIPv4.String() != "127.0.0.1" {
		t.Fatalf("CallbackIPv4 = %v", cfg.CallbackIPv4)
	}
	if cfg.CallbackIPv6 == nil || cfg.CallbackIPv6.String() != "::1" {
		t.Fatalf("CallbackIPv6 = %v", cfg.CallbackIPv6)
	}
	if len(cfg.AuthorizedScopes) != 2 || cfg.AuthorizedScopes[1] != "127.0.0.1" {
		t.Fatalf("AuthorizedScopes = %#v", cfg.AuthorizedScopes)
	}
	if cfg.MaxTestLifetime != time.Hour || cfg.DefaultTestTTL != 15*time.Minute {
		t.Fatalf("lifetimes = %s / %s", cfg.MaxTestLifetime, cfg.DefaultTestTTL)
	}
	if got := cfg.HTTPListenAddr(); got != "127.0.0.1:8081" {
		t.Fatalf("HTTPListenAddr() = %q", got)
	}
	if got := cfg.DNSListenAddr(); got != "127.0.0.1:5353" {
		t.Fatalf("DNSListenAddr() = %q", got)
	}
	if cfg.MCPTransport != MCPTransportOff {
		t.Fatalf("MCPTransport = %q", cfg.MCPTransport)
	}
	if got := cfg.MCPHTTPListenAddr(); got != "127.0.0.1:8082" {
		t.Fatalf("MCPHTTPListenAddr() = %q", got)
	}
	if cfg.MCPHTTPPath != "/mcp" {
		t.Fatalf("MCPHTTPPath = %q", cfg.MCPHTTPPath)
	}
}

func TestLoadRequiresAuthorizedScopes(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{"OAST_AUTHORIZED_SCOPES": ""},
		{"OAST_AUTHORIZED_SCOPES": " , "},
	} {
		if _, err := LoadFromEnv(envLookup(values)); err == nil || !strings.Contains(err.Error(), "OAST_AUTHORIZED_SCOPES") {
			t.Fatalf("LoadFromEnv(%#v) error = %v, want missing-scope error", values, err)
		}
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"OAST_AUTHORIZED_SCOPES": "lab.local"}
	}
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"invalid scope", func(m map[string]string) { m["OAST_AUTHORIZED_SCOPES"] = "https://lab.local" }, "OAST_AUTHORIZED_SCOPES"},
		{"bad http port", func(m map[string]string) { m["OAST_HTTP_PORT"] = "70000" }, "OAST_HTTP_PORT"},
		{"bad dns port", func(m map[string]string) { m["OAST_DNS_PORT"] = "-1" }, "OAST_DNS_PORT"},
		{"bad listen host", func(m map[string]string) { m["OAST_HTTP_ADDR"] = "bad_host" }, "OAST_HTTP_ADDR"},
		{"bad domain", func(m map[string]string) { m["OAST_PUBLIC_DOMAIN"] = "https://oast.local" }, "OAST_PUBLIC_DOMAIN"},
		{"domain is ip", func(m map[string]string) { m["OAST_PUBLIC_DOMAIN"] = "127.0.0.1" }, "OAST_PUBLIC_DOMAIN"},
		{"bad ipv4", func(m map[string]string) { m["OAST_CALLBACK_IPV4"] = "::1" }, "OAST_CALLBACK_IPV4"},
		{"bad ipv6", func(m map[string]string) { m["OAST_CALLBACK_IPV6"] = "127.0.0.1" }, "OAST_CALLBACK_IPV6"},
		{"no callback ip", func(m map[string]string) {
			m["OAST_CALLBACK_IPV4"] = ""
			m["OAST_CALLBACK_IPV6"] = ""
		}, "at least one"},
		{"default ttl too long", func(m map[string]string) {
			m["OAST_MAX_TEST_LIFETIME"] = "1h"
			m["OAST_DEFAULT_TEST_TTL"] = "2h"
		}, "OAST_DEFAULT_TEST_TTL"},
		{"lifetime too short", func(m map[string]string) { m["OAST_MAX_TEST_LIFETIME"] = "30s" }, "OAST_MAX_TEST_LIFETIME"},
		{"body too large", func(m map[string]string) { m["OAST_MAX_BODY_BYTES"] = "999999999" }, "OAST_MAX_BODY_BYTES"},
		{"zero active tests", func(m map[string]string) { m["OAST_MAX_ACTIVE_TESTS"] = "0" }, "OAST_MAX_ACTIVE_TESTS"},
		{"bad rate", func(m map[string]string) { m["OAST_RATE_LIMIT_PER_SECOND"] = "0" }, "OAST_RATE_LIMIT_PER_SECOND"},
		{"bad mcp transport", func(m map[string]string) { m["OAST_MCP_TRANSPORT"] = "sse" }, "OAST_MCP_TRANSPORT"},
		{"public mcp host", func(m map[string]string) { m["OAST_MCP_HTTP_ADDR"] = "0.0.0.0" }, "OAST_MCP_HTTP_ADDR"},
		{"bad mcp port", func(m map[string]string) { m["OAST_MCP_HTTP_PORT"] = "0" }, "OAST_MCP_HTTP_PORT"},
		{"bad mcp path", func(m map[string]string) { m["OAST_MCP_HTTP_PATH"] = "mcp" }, "OAST_MCP_HTTP_PATH"},
		{"mcp path query", func(m map[string]string) { m["OAST_MCP_HTTP_PATH"] = "/mcp?x=1" }, "OAST_MCP_HTTP_PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := base()
			tt.mutate(values)
			_, err := LoadFromEnv(envLookup(values))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFromEnv() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadCustomValues(t *testing.T) {
	cfg, err := LoadFromEnv(envLookup(map[string]string{
		"OAST_HTTP_ADDR":                   "::1",
		"OAST_HTTP_PORT":                   "9090",
		"OAST_DNS_ADDR":                    "::1",
		"OAST_DNS_PORT":                    "15353",
		"OAST_PUBLIC_DOMAIN":               "Callbacks.Example.Test.",
		"OAST_CALLBACK_IPV4":               "192.0.2.5",
		"OAST_CALLBACK_IPV6":               "2001:db8::5",
		"OAST_AUTHORIZED_SCOPES":           "*.lab.example.test,10.0.0.0/8",
		"OAST_MAX_TEST_LIFETIME":           "2h",
		"OAST_DEFAULT_TEST_TTL":            "90m",
		"OAST_MAX_ACTIVE_TESTS":            "7",
		"OAST_MAX_BODY_BYTES":              "1234",
		"OAST_MAX_HEADER_BYTES":            "2048",
		"OAST_MAX_QUERY_BYTES":             "512",
		"OAST_MAX_EVIDENCE_PER_TEST":       "9",
		"OAST_MAX_EVIDENCE_BYTES_PER_TEST": "4096",
		"OAST_RATE_LIMIT_PER_SECOND":       "12.5",
		"OAST_RATE_LIMIT_BURST":            "13",
		"OAST_MCP_TRANSPORT":               "BOTH",
		"OAST_MCP_HTTP_ADDR":               "::1",
		"OAST_MCP_HTTP_PORT":               "9082",
		"OAST_MCP_HTTP_PATH":               "/mcp/v1",
	}))
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.PublicDomain != "callbacks.example.test" {
		t.Fatalf("PublicDomain = %q", cfg.PublicDomain)
	}
	if cfg.HTTPListenAddr() != "[::1]:9090" || cfg.DNSListenAddr() != "[::1]:15353" {
		t.Fatalf("listen addresses = %q / %q", cfg.HTTPListenAddr(), cfg.DNSListenAddr())
	}
	if cfg.CallbackIPv4.String() != "192.0.2.5" || cfg.CallbackIPv6.String() != "2001:db8::5" {
		t.Fatalf("callback IPs = %v / %v", cfg.CallbackIPv4, cfg.CallbackIPv6)
	}
	if cfg.MaxActiveTests != 7 || cfg.MaxBodyBytes != 1234 || cfg.MaxEvidencePerTest != 9 {
		t.Fatalf("custom limits not applied: %#v", cfg)
	}
	if cfg.RateLimitPerSecond != 12.5 || cfg.RateLimitBurst != 13 {
		t.Fatalf("rate limit not applied: %v/%d", cfg.RateLimitPerSecond, cfg.RateLimitBurst)
	}
	if cfg.MCPTransport != MCPTransportBoth || cfg.MCPHTTPPath != "/mcp/v1" {
		t.Fatalf("MCP config = %#v", cfg)
	}
	if got := cfg.MCPHTTPListenAddr(); got != "[::1]:9082" {
		t.Fatalf("MCPHTTPListenAddr() = %q", got)
	}
}
