// Package lab is an INTENTIONALLY VULNERABLE application used only to
// demonstrate out-of-band (OAST) callbacks in a controlled, local environment.
//
// WARNING: This application deliberately performs server-side requests on
// behalf of a caller (an SSRF-style behavior) and server-side DNS lookups. It
// exists so that an operator or an MCP-driven AI agent can observe an
// out-of-band interaction reaching the local OAST callback receiver. It is NOT
// a general-purpose fetcher, proxy, or resolver.
//
// Hard safety boundaries enforced here, not merely by convention:
//
//   - Both demo behaviors are DISABLED by default and must be explicitly
//     enabled through configuration.
//   - The HTTP fetcher only accepts destinations whose host is inside the
//     configured callback zone, and its network dialer is PINNED to a single
//     configured loopback target. It can never open a connection to any other
//     address, so it cannot probe private networks, cloud metadata endpoints,
//     or arbitrary hosts.
//   - Redirects are never followed.
//   - The DNS lookup only accepts hostnames inside the configured callback
//     zone, and its resolver is PINNED to a single configured DNS address.
//   - Short timeouts and a response-size cap apply. No caller credentials or
//     headers are forwarded.
//
// This package imports nothing from the OAST engine. It is a stand-alone
// "victim" that the engine observes.
package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config holds the lab's validated, local-only configuration.
type Config struct {
	// ListenAddr is the lab's own HTTP listen address.
	ListenAddr string
	// CallbackZone is the only DNS zone the demos will touch, e.g. "oast.local".
	CallbackZone string
	// OASTHTTPTarget is the fixed host:port the HTTP fetcher always dials,
	// regardless of the requested URL host (the local OAST HTTP receiver).
	OASTHTTPTarget string
	// OASTDNSAddr is the fixed host:port the DNS resolver always queries
	// (the local OAST authoritative DNS receiver).
	OASTDNSAddr string

	// EnableHTTPCallback enables Scenario A. Disabled by default.
	EnableHTTPCallback bool
	// EnableDNSCallback enables Scenario B. Disabled by default.
	EnableDNSCallback bool

	// HTTPTimeout bounds each outbound fetch.
	HTTPTimeout time.Duration
	// DNSTimeout bounds each outbound DNS query.
	DNSTimeout time.Duration
	// MaxResponseBytes caps how much of a fetched response body is read.
	MaxResponseBytes int64
}

// Defaults returns safe, local-only defaults with both demos disabled.
func Defaults() Config {
	return Config{
		ListenAddr:         "127.0.0.1:8090",
		CallbackZone:       "oast.local",
		OASTHTTPTarget:     "127.0.0.1:8081",
		OASTDNSAddr:        "127.0.0.1:5353",
		EnableHTTPCallback: false,
		EnableDNSCallback:  false,
		HTTPTimeout:        3 * time.Second,
		DNSTimeout:         3 * time.Second,
		MaxResponseBytes:   4096,
	}
}

// LoadFromEnv builds a Config from a lookup function (os.LookupEnv in main).
func LoadFromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Defaults()

	cfg.ListenAddr = str(lookup, "LAB_LISTEN_ADDR", cfg.ListenAddr)
	cfg.OASTHTTPTarget = str(lookup, "LAB_OAST_HTTP_TARGET", cfg.OASTHTTPTarget)
	cfg.OASTDNSAddr = str(lookup, "LAB_OAST_DNS_ADDR", cfg.OASTDNSAddr)

	zone := strings.ToLower(strings.TrimSuffix(str(lookup, "LAB_CALLBACK_ZONE", cfg.CallbackZone), "."))
	if zone == "" || net.ParseIP(zone) != nil || !strings.Contains(zone, ".") {
		return Config{}, fmt.Errorf("LAB_CALLBACK_ZONE must be a dotted DNS name, got %q", zone)
	}
	cfg.CallbackZone = zone

	var err error
	if cfg.EnableHTTPCallback, err = boolEnv(lookup, "LAB_ENABLE_HTTP_CALLBACK", cfg.EnableHTTPCallback); err != nil {
		return Config{}, err
	}
	if cfg.EnableDNSCallback, err = boolEnv(lookup, "LAB_ENABLE_DNS_CALLBACK", cfg.EnableDNSCallback); err != nil {
		return Config{}, err
	}
	if cfg.HTTPTimeout, err = durEnv(lookup, "LAB_HTTP_TIMEOUT", cfg.HTTPTimeout); err != nil {
		return Config{}, err
	}
	if cfg.DNSTimeout, err = durEnv(lookup, "LAB_DNS_TIMEOUT", cfg.DNSTimeout); err != nil {
		return Config{}, err
	}
	if cfg.MaxResponseBytes, err = int64Env(lookup, "LAB_MAX_RESPONSE_BYTES", cfg.MaxResponseBytes); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("LAB_LISTEN_ADDR %q is not host:port: %w", c.ListenAddr, err)
	}
	if err := requireLoopbackHostPort("LAB_OAST_HTTP_TARGET", c.OASTHTTPTarget); err != nil {
		return err
	}
	if err := requireLoopbackHostPort("LAB_OAST_DNS_ADDR", c.OASTDNSAddr); err != nil {
		return err
	}
	if c.HTTPTimeout <= 0 || c.HTTPTimeout > 30*time.Second {
		return fmt.Errorf("LAB_HTTP_TIMEOUT must be between 1ns and 30s")
	}
	if c.DNSTimeout <= 0 || c.DNSTimeout > 30*time.Second {
		return fmt.Errorf("LAB_DNS_TIMEOUT must be between 1ns and 30s")
	}
	if c.MaxResponseBytes < 1 || c.MaxResponseBytes > 1<<20 {
		return fmt.Errorf("LAB_MAX_RESPONSE_BYTES must be between 1 and 1048576")
	}
	return nil
}

// requireLoopbackHostPort fails closed unless addr is a loopback host:port.
// The demo must never be pointed at a non-loopback destination.
func requireLoopbackHostPort(key, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q is not host:port: %w", key, addr, err)
	}
	if port == "" {
		return fmt.Errorf("%s %q must include a port", key, addr)
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("%s must be a loopback address, got %q", key, host)
		}
		return nil
	}
	if strings.ToLower(host) != "localhost" {
		return fmt.Errorf("%s must be a loopback address or localhost, got %q", key, host)
	}
	return nil
}

// Server is the intentionally vulnerable lab HTTP application.
type Server struct {
	cfg      Config
	logger   *slog.Logger
	client   *http.Client
	resolver *net.Resolver
}

// NewServer builds the lab server with a pinned HTTP dialer and DNS resolver.
func NewServer(cfg Config, logger *slog.Logger) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// The transport ignores the request's host entirely and always dials the
	// single configured OAST target. This is the core containment control for
	// Scenario A: the fetcher cannot reach any other address.
	target := cfg.OASTHTTPTarget
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		MaxIdleConns:          1,
		ResponseHeaderTimeout: cfg.HTTPTimeout,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: cfg.HTTPTimeout}
			return d.DialContext(ctx, "tcp", target)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.HTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("lab does not follow redirects")
		},
	}

	dnsAddr := cfg.OASTDNSAddr
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: cfg.DNSTimeout}
			// Preserve udp/tcp but force the configured DNS server address.
			return d.DialContext(ctx, network, dnsAddr)
		},
	}

	return &Server{cfg: cfg, logger: logger, client: client, resolver: resolver}, nil
}

// Handler returns the lab's HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /lab/http-fetch", s.handleHTTPFetch)
	mux.HandleFunc("POST /lab/dns-lookup", s.handleDNSLookup)
	mux.HandleFunc("GET /", s.handleRoot)
	return mux
}

type healthResponse struct {
	Status             string `json:"status"`
	Warning            string `json:"warning"`
	CallbackZone       string `json:"callback_zone"`
	HTTPCallbackReady  bool   `json:"http_callback_enabled"`
	DNSCallbackReady   bool   `json:"dns_callback_enabled"`
	OASTHTTPTarget     string `json:"oast_http_target"`
	OASTDNSAddr        string `json:"oast_dns_addr"`
	MaxResponseBytes   int64  `json:"max_response_bytes"`
	TimeoutMillisecond int64  `json:"timeout_ms"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:             "ok",
		Warning:            "intentionally vulnerable lab; local testing only",
		CallbackZone:       s.cfg.CallbackZone,
		HTTPCallbackReady:  s.cfg.EnableHTTPCallback,
		DNSCallbackReady:   s.cfg.EnableDNSCallback,
		OASTHTTPTarget:     s.cfg.OASTHTTPTarget,
		OASTDNSAddr:        s.cfg.OASTDNSAddr,
		MaxResponseBytes:   s.cfg.MaxResponseBytes,
		TimeoutMillisecond: s.cfg.HTTPTimeout.Milliseconds(),
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "oast-lab",
		"warning": "INTENTIONALLY VULNERABLE. Local OAST demonstration only. Do not deploy.",
		"endpoints": []string{
			"GET  /healthz",
			"POST /lab/http-fetch  {\"url\": \"http://<token>." + s.cfg.CallbackZone + ":<port>/...\"}",
			"POST /lab/dns-lookup  {\"hostname\": \"<token>." + s.cfg.CallbackZone + "\"}",
		},
	})
}

type errorResponse struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

// --- Scenario A: controlled HTTP callback -----------------------------------

type httpFetchRequest struct {
	URL string `json:"url"`
}

type httpFetchResponse struct {
	Scenario    string `json:"scenario"`
	Note        string `json:"note"`
	RequestedTo string `json:"requested_to"`
	DialedTo    string `json:"dialed_to"`
	StatusCode  int    `json:"status_code"`
	BytesRead   int    `json:"bytes_read"`
	Truncated   bool   `json:"truncated"`
}

func (s *Server) handleHTTPFetch(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableHTTPCallback {
		s.logger.Warn("http-fetch rejected: demo disabled")
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "the HTTP callback demo is disabled",
			Hint:  "set LAB_ENABLE_HTTP_CALLBACK=true to enable it for local testing",
		})
		return
	}

	var req httpFetchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	target, err := s.validateCallbackURL(req.URL)
	if err != nil {
		s.logger.Warn("http-fetch rejected: destination not allowed", "url", req.URL, "error", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "destination not permitted by lab allowlist: " + err.Error(),
			Hint:  "the host must be a single label inside the callback zone " + s.cfg.CallbackZone,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.HTTPTimeout)
	defer cancel()

	// Build a fresh request. No caller headers or credentials are forwarded.
	outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not build request"})
		return
	}
	outReq.Header.Set("User-Agent", "oast-lab/scenario-a")

	s.logger.Info("http-fetch triggering out-of-band HTTP callback",
		"host", target.Host, "dialed_to", s.cfg.OASTHTTPTarget)

	resp, err := s.client.Do(outReq)
	if err != nil {
		s.logger.Error("http-fetch failed", "error", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "the OAST callback receiver could not be reached: " + err.Error(),
			Hint:  "is oastd running and listening on " + s.cfg.OASTHTTPTarget + "?",
		})
		return
	}
	defer resp.Body.Close()

	body, truncated, _ := readCapped(resp.Body, s.cfg.MaxResponseBytes)
	writeJSON(w, http.StatusOK, httpFetchResponse{
		Scenario:    "A: controlled HTTP callback",
		Note:        "an out-of-band HTTP request was sent; observe it via oast_get_evidence (protocol http)",
		RequestedTo: target.Host,
		DialedTo:    s.cfg.OASTHTTPTarget,
		StatusCode:  resp.StatusCode,
		BytesRead:   len(body),
		Truncated:   truncated,
	})
}

// validateCallbackURL enforces the Scenario A allowlist.
func (s *Server) validateCallbackURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("url is not parseable")
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("scheme must be http, got %q", u.Scheme)
	}
	if u.User != nil {
		return nil, errors.New("url must not contain credentials")
	}
	if u.Hostname() == "" {
		return nil, errors.New("url must contain a host")
	}
	if err := s.hostInZone(u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// --- Scenario B: controlled DNS callback ------------------------------------

type dnsLookupRequest struct {
	Hostname string `json:"hostname"`
}

type dnsLookupResponse struct {
	Scenario  string   `json:"scenario"`
	Note      string   `json:"note"`
	Hostname  string   `json:"hostname"`
	QueriedAt string   `json:"queried_at"`
	Addresses []string `json:"addresses"`
	Resolved  bool     `json:"resolved"`
	Detail    string   `json:"detail,omitempty"`
}

func (s *Server) handleDNSLookup(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableDNSCallback {
		s.logger.Warn("dns-lookup rejected: demo disabled")
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "the DNS callback demo is disabled",
			Hint:  "set LAB_ENABLE_DNS_CALLBACK=true to enable it for local testing",
		})
		return
	}

	var req dnsLookupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	host := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(req.Hostname, ".")))
	if err := s.hostInZone(host); err != nil {
		s.logger.Warn("dns-lookup rejected: hostname not allowed", "hostname", req.Hostname, "error", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "hostname not permitted by lab allowlist: " + err.Error(),
			Hint:  "the host must be a single label inside the callback zone " + s.cfg.CallbackZone,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.DNSTimeout)
	defer cancel()

	fqdn := host + "." // absolute name; no search-list expansion
	s.logger.Info("dns-lookup triggering out-of-band DNS callback",
		"hostname", host, "resolver", s.cfg.OASTDNSAddr)

	addrs, lookupErr := s.resolver.LookupHost(ctx, fqdn)

	out := dnsLookupResponse{
		Scenario:  "B: controlled DNS callback",
		Note:      "an out-of-band DNS query was sent; observe it via oast_get_evidence (protocol dns)",
		Hostname:  host,
		QueriedAt: time.Now().UTC().Format(time.RFC3339),
		Addresses: addrs,
		Resolved:  lookupErr == nil,
	}
	if lookupErr != nil {
		var dnsErr *net.DNSError
		if errors.As(lookupErr, &dnsErr) && dnsErr.IsNotFound {
			// The query was still sent to (and recorded by) the receiver; the
			// zone simply returned no address for an unknown/closed token.
			out.Detail = "no address returned (NXDOMAIN); the query was still sent"
			writeJSON(w, http.StatusOK, out)
			return
		}
		s.logger.Error("dns-lookup failed to reach resolver", "error", lookupErr)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "the OAST DNS receiver could not be reached: " + lookupErr.Error(),
			Hint:  "is oastd running and listening for DNS on " + s.cfg.OASTDNSAddr + "?",
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// hostInZone enforces that host is "<single-label>.<zone>".
func (s *Server) hostInZone(host string) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return errors.New("host must be a name inside the callback zone, not an IP")
	}
	suffix := "." + s.cfg.CallbackZone
	if !strings.HasSuffix(host, suffix) {
		return fmt.Errorf("host %q is outside the callback zone %q", host, s.cfg.CallbackZone)
	}
	label := strings.TrimSuffix(host, suffix)
	if label == "" || strings.Contains(label, ".") {
		return errors.New("host must be exactly one label under the callback zone")
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func readCapped(r io.Reader, max int64) (body []byte, truncated bool, err error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > max {
		return buf[:max], true, nil
	}
	return buf, false, nil
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("request body must be a small JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func str(lookup func(string) (string, bool), key, def string) string {
	if v, ok := lookup(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

func boolEnv(lookup func(string) (string, bool), key string, def bool) (bool, error) {
	v, ok := lookup(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean (true/false)", key)
	}
}

func durEnv(lookup func(string) (string, bool), key string, def time.Duration) (time.Duration, error) {
	v, ok := lookup(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration (e.g. 3s): %w", key, err)
	}
	return d, nil
}

func int64Env(lookup func(string) (string, bool), key string, def int64) (int64, error) {
	v, ok := lookup(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	var n int64
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return n, nil
}
