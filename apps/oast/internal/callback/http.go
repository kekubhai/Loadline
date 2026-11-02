package callback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/ratelimit"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

const (
	maxHeaderValueBytes = 2048
	maxContentTypeBytes = 1024
)

var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"api-key":             true,
}

// HTTPConfig controls the callback HTTP receiver.
type HTTPConfig struct {
	PublicDomain   string
	MaxBodyBytes   int64
	MaxHeaderBytes int
	MaxQueryBytes  int
}

// HTTPHandler records callbacks and always returns a static response.
type HTTPHandler struct {
	cfg     HTTPConfig
	sink    registry.EvidenceSink
	limiter *ratelimit.Limiter
	logger  *slog.Logger
}

// NewHTTPHandler validates cfg and returns a receiver. limiter may be nil
// in tests; production should always provide one.
func NewHTTPHandler(cfg HTTPConfig, sink registry.EvidenceSink, limiter *ratelimit.Limiter, logger *slog.Logger) (*HTTPHandler, error) {
	if sink == nil {
		return nil, fmt.Errorf("http callback: evidence sink is required")
	}
	domain, err := scope.NormalizeHostname(cfg.PublicDomain)
	if err != nil {
		return nil, fmt.Errorf("http callback: public domain: %w", err)
	}
	if net.ParseIP(domain) != nil {
		return nil, fmt.Errorf("http callback: public domain must be a DNS name")
	}
	if cfg.MaxBodyBytes <= 0 {
		return nil, fmt.Errorf("http callback: MaxBodyBytes must be > 0")
	}
	if cfg.MaxHeaderBytes <= 0 {
		return nil, fmt.Errorf("http callback: MaxHeaderBytes must be > 0")
	}
	if cfg.MaxQueryBytes <= 0 {
		return nil, fmt.Errorf("http callback: MaxQueryBytes must be > 0")
	}
	cfg.PublicDomain = domain
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &HTTPHandler{cfg: cfg, sink: sink, limiter: limiter, logger: logger}, nil
}

// ServeHTTP implements http.Handler.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.limiter != nil && !h.limiter.Allow() {
		writeStatic(w, http.StatusTooManyRequests, "rate limited\n")
		return
	}
	if len(r.RequestURI) > h.cfg.MaxHeaderBytes {
		writeStatic(w, http.StatusRequestURITooLong, "request too large\n")
		return
	}
	if requestHeaderBytes(r) > h.cfg.MaxHeaderBytes {
		writeStatic(w, http.StatusRequestHeaderFieldsTooLarge, "headers too large\n")
		return
	}

	callbackToken, normalizedHost, ok := parseCallbackHost(r.Host, h.cfg.PublicDomain)
	if !ok {
		writeStatic(w, http.StatusNotFound, "not found\n")
		return
	}

	var body []byte
	var bodyTruncated bool
	if r.Body != nil {
		var err error
		body, bodyTruncated, err = readLimited(r.Body, h.cfg.MaxBodyBytes)
		if err != nil {
			writeStatic(w, http.StatusBadRequest, "bad request\n")
			return
		}
		defer r.Body.Close()
	}

	rawQuery := r.URL.RawQuery
	queryTruncated := false
	if len(rawQuery) > h.cfg.MaxQueryBytes {
		rawQuery = rawQuery[:h.cfg.MaxQueryBytes]
		queryTruncated = true
	}
	query, queryErr := url.ParseQuery(rawQuery)
	if queryErr != nil {
		query = nil
	}

	headers, headersRedacted, headersTruncated := captureHeaders(r.Header, h.cfg.MaxHeaderBytes)
	contentType := truncate(r.Header.Get("Content-Type"), maxContentTypeBytes)

	record := evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		SourceIP: remoteIP(r.RemoteAddr),
		HTTP: &evidence.HTTPInteraction{
			Method:                r.Method,
			Host:                  normalizedHost,
			RawHost:               r.Host,
			Path:                  escapedPath(r.URL),
			RawQuery:              rawQuery,
			Query:                 query,
			QueryTruncated:        queryTruncated,
			Headers:               headers,
			HeadersRedacted:       headersRedacted,
			HeadersTruncated:      headersTruncated,
			ContentType:           contentType,
			Body:                  body,
			BodySHA256:            evidence.HashBody(body),
			BodyCapturedBytes:     len(body),
			DeclaredContentLength: r.ContentLength,
			BodyTruncated:         bodyTruncated,
		},
	}
	if len(body) == 0 {
		record.HTTP.Body = nil
	}

	_, appendErr := h.sink.AppendEvidence(r.Context(), callbackToken, record)
	switch {
	case appendErr == nil:
		writeStatic(w, http.StatusOK, "ok\n")
	case errors.Is(appendErr, registry.ErrNotFound),
		errors.Is(appendErr, registry.ErrClosed),
		errors.Is(appendErr, registry.ErrExpired):
		writeStatic(w, http.StatusNotFound, "not found\n")
	case errors.Is(appendErr, context.Canceled),
		errors.Is(appendErr, context.DeadlineExceeded):
		writeStatic(w, http.StatusServiceUnavailable, "unavailable\n")
	default:
		// Evidence caps and other internal conditions must not disclose
		// whether the token exists. The interaction is silently dropped.
		h.logger.Warn("http callback evidence not recorded", "error", appendErr)
		writeStatic(w, http.StatusOK, "ok\n")
	}
}

func parseCallbackHost(rawHost, zone string) (callbackToken, normalizedHost string, ok bool) {
	if rawHost == "" || len(rawHost) > 300 {
		return "", "", false
	}
	if strings.ContainsAny(rawHost, "/\\@, \t\r\n") {
		return "", "", false
	}
	host := rawHost
	if strings.HasPrefix(host, "[") {
		return "", "", false
	}
	switch strings.Count(host, ":") {
	case 0:
	case 1:
		hostPart, port, err := net.SplitHostPort(host)
		if err != nil || port == "" {
			return "", "", false
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", "", false
		}
		host = hostPart
	default:
		return "", "", false
	}

	normalized, err := scope.NormalizeHostname(host)
	if err != nil {
		return "", "", false
	}
	suffix := "." + zone
	if !strings.HasSuffix(normalized, suffix) {
		return "", "", false
	}
	label := strings.TrimSuffix(normalized, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", "", false
	}
	if !token.IsValid(label) {
		return "", "", false
	}
	return label, normalized, true
}

func requestHeaderBytes(r *http.Request) int {
	n := len(r.Method) + len(r.RequestURI) + len(r.Proto)
	for name, values := range r.Header {
		n += len(name) + 2
		for _, value := range values {
			n += len(value) + 2
		}
	}
	return n
}

func readLimited(r io.Reader, max int64) (body []byte, truncated bool, err error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > max {
		return buf[:max], true, nil
	}
	return buf, false, nil
}

func captureHeaders(headers http.Header, maxBytes int) (map[string][]string, bool, bool) {
	if len(headers) == 0 {
		return nil, false, false
	}
	out := make(map[string][]string, len(headers))
	redacted := false
	truncated := false
	remaining := maxBytes

	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if remaining <= 0 {
			truncated = true
			break
		}
		values := headers[name]
		lower := strings.ToLower(name)
		for _, value := range values {
			if remaining <= 0 {
				truncated = true
				break
			}
			if sensitiveHeaders[lower] {
				redacted = true
				value = "[REDACTED]"
			} else if len(value) > maxHeaderValueBytes || len(value) > remaining {
				value = truncate(value, minInt(maxHeaderValueBytes, remaining))
				truncated = true
			}
			if remaining >= len(value) {
				out[name] = append(out[name], value)
				remaining -= len(value)
			} else {
				truncated = true
			}
		}
	}
	return out, redacted, truncated
}

func escapedPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if p := u.EscapedPath(); p != "" {
		return p
	}
	return "/"
}

func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func writeStatic(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
