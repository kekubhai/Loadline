// Package scope parses and enforces the authorized-target allowlist.
//
// Scope matching is declarative. It never resolves DNS, opens connections,
// or verifies that the caller really owns the target. The authorization
// reference is caller-provided context, not proof of authorization.
package scope

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

var (
	// ErrNoScopes means no authorized scopes were configured.
	ErrNoScopes = errors.New("no authorized scopes configured")
	// ErrInvalidScope means a configured scope pattern is malformed.
	ErrInvalidScope = errors.New("invalid authorized scope")
	// ErrInvalidTarget means the target is not a well-formed host or URL.
	ErrInvalidTarget = errors.New("invalid target")
	// ErrOutOfScope means the target is valid but not authorized.
	ErrOutOfScope = errors.New("target is outside the authorized scope")
)

// Target is a normalized, but not yet authorized, test target.
type Target struct {
	Raw  string
	Host string
	Port string
	IP   net.IP
}

// IsIP reports whether the target is a literal IP address.
func (t Target) IsIP() bool { return t.IP != nil }

type ruleKind uint8

const (
	ruleHost ruleKind = iota
	ruleWildcardHost
	ruleIP
	ruleCIDR
)

type rule struct {
	kind    ruleKind
	host    string
	ip      net.IP
	network *net.IPNet
}

// Validator matches targets against configured scope patterns.
type Validator struct {
	rules []rule
}

// New parses scope patterns. Accepted forms:
//
//	example.com          exact hostname
//	*.example.com        any subdomain (not the apex)
//	192.0.2.10           exact IPv4/IPv6 address
//	10.0.0.0/8           CIDR range
//
// URLs are intentionally not accepted as scope patterns.
func New(patterns []string) (*Validator, error) {
	if len(patterns) == 0 {
		return nil, ErrNoScopes
	}
	v := &Validator{}
	for _, pattern := range patterns {
		p := strings.TrimSpace(pattern)
		if p == "" {
			return nil, fmt.Errorf("%w: empty pattern", ErrInvalidScope)
		}
		r, err := parseRule(p)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %v", ErrInvalidScope, pattern, err)
		}
		v.rules = append(v.rules, r)
	}
	if len(v.rules) == 0 {
		return nil, ErrNoScopes
	}
	return v, nil
}

func parseRule(pattern string) (rule, error) {
	if strings.ContainsAny(pattern, " \t\r\n") {
		return rule{}, errors.New("whitespace is not allowed")
	}
	if _, network, err := net.ParseCIDR(pattern); err == nil {
		return rule{kind: ruleCIDR, network: network}, nil
	}
	if ip := net.ParseIP(pattern); ip != nil {
		return rule{kind: ruleIP, ip: ip}, nil
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		host, err := NormalizeHostname(suffix)
		if err != nil {
			return rule{}, err
		}
		if net.ParseIP(host) != nil {
			return rule{}, errors.New("wildcard cannot target an IP address")
		}
		return rule{kind: ruleWildcardHost, host: host}, nil
	}
	if strings.Contains(pattern, "*") {
		return rule{}, errors.New("wildcard must be the leading '*.' label")
	}
	if strings.ContainsAny(pattern, "/:@?#") {
		return rule{}, errors.New("scope patterns must be hosts, IPs, or CIDRs")
	}
	host, err := NormalizeHostname(pattern)
	if err != nil {
		return rule{}, err
	}
	return rule{kind: ruleHost, host: host}, nil
}

// Authorize parses raw and returns it when it matches an authorized scope.
func (v *Validator) Authorize(raw string) (Target, error) {
	target, err := ParseTarget(raw)
	if err != nil {
		return Target{}, err
	}
	if !v.Matches(target) {
		return Target{}, fmt.Errorf("%w: %s", ErrOutOfScope, target.Host)
	}
	return target, nil
}

// Matches reports whether target is inside the configured allowlist.
func (v *Validator) Matches(target Target) bool {
	for _, r := range v.rules {
		switch r.kind {
		case ruleIP:
			if target.IP != nil && r.ip.Equal(target.IP) {
				return true
			}
		case ruleCIDR:
			if target.IP != nil && r.network.Contains(target.IP) {
				return true
			}
		case ruleHost:
			if target.IP == nil && target.Host == r.host {
				return true
			}
		case ruleWildcardHost:
			if target.IP == nil && target.Host != r.host &&
				strings.HasSuffix(target.Host, "."+r.host) {
				return true
			}
		}
	}
	return false
}

// ParseTarget accepts an http(s) URL or a bare host, host:port, IP, or
// bracketed IPv6 address. URL paths and queries identify the target but do
// not affect scope matching.
func ParseTarget(raw string) (Target, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Target{}, fmt.Errorf("%w: target is required", ErrInvalidTarget)
	}
	if len(s) > 2048 {
		return Target{}, fmt.Errorf("%w: target is too long", ErrInvalidTarget)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return Target{}, fmt.Errorf("%w: control character in target", ErrInvalidTarget)
		}
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return Target{}, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return Target{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidTarget, u.Scheme)
		}
		if u.User != nil {
			return Target{}, fmt.Errorf("%w: userinfo is not allowed", ErrInvalidTarget)
		}
		into, err := targetFromHostPort(u.Hostname(), u.Port())
		if err != nil {
			return Target{}, err
		}
		into.Raw = s
		return into, nil
	}

	if strings.ContainsAny(s, "/?#") {
		return Target{}, fmt.Errorf("%w: bare targets cannot contain a path or query", ErrInvalidTarget)
	}

	host, port, ip, err := parseBare(s)
	if err != nil {
		return Target{}, err
	}
	if ip != nil {
		return Target{Raw: s, Host: ip.String(), Port: port, IP: ip}, nil
	}
	into, err := targetFromHostPort(host, port)
	if err != nil {
		return Target{}, err
	}
	into.Raw = s
	return into, nil
}

func parseBare(s string) (host, port string, ip net.IP, err error) {
	if parsed := net.ParseIP(s); parsed != nil {
		return "", "", parsed, nil
	}
	if strings.HasPrefix(s, "[") {
		h, p, splitErr := net.SplitHostPort(s)
		if splitErr != nil {
			if strings.HasSuffix(s, "]") {
				inner := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
				parsed := net.ParseIP(inner)
				if parsed == nil {
					return "", "", nil, fmt.Errorf("%w: malformed IPv6 literal", ErrInvalidTarget)
				}
				return "", "", parsed, nil
			}
			return "", "", nil, fmt.Errorf("%w: malformed IPv6 target", ErrInvalidTarget)
		}
		parsed := net.ParseIP(h)
		if parsed == nil {
			return "", "", nil, fmt.Errorf("%w: malformed IPv6 literal", ErrInvalidTarget)
		}
		if err := validatePort(p); err != nil {
			return "", "", nil, err
		}
		return "", p, parsed, nil
	}
	if strings.Count(s, ":") == 1 {
		h, p, splitErr := net.SplitHostPort(s)
		if splitErr != nil {
			return "", "", nil, fmt.Errorf("%w: %v", ErrInvalidTarget, splitErr)
		}
		if err := validatePort(p); err != nil {
			return "", "", nil, err
		}
		return h, p, nil, nil
	}
	if strings.Contains(s, ":") {
		return "", "", nil, fmt.Errorf("%w: malformed host or IPv6 address", ErrInvalidTarget)
	}
	return s, "", nil, nil
}

func targetFromHostPort(host, port string) (Target, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return Target{}, fmt.Errorf("%w: host is required", ErrInvalidTarget)
	}
	if port != "" {
		if err := validatePort(port); err != nil {
			return Target{}, err
		}
	}
	if strings.Contains(host, "%") {
		return Target{}, fmt.Errorf("%w: IPv6 zone identifiers are not allowed", ErrInvalidTarget)
	}
	into := Target{Port: port}
	if ip := net.ParseIP(host); ip != nil {
		into.IP = ip
		into.Host = ip.String()
		return into, nil
	}
	normalized, err := NormalizeHostname(host)
	if err != nil {
		return Target{}, err
	}
	into.Host = normalized
	return into, nil
}

func validatePort(port string) error {
	if port == "" {
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%w: invalid port %q", ErrInvalidTarget, port)
	}
	return nil
}

// NormalizeHostname lower-cases a hostname, strips one trailing dot, and
// validates its labels. It does not resolve DNS.
func NormalizeHostname(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("%w: host is empty", ErrInvalidTarget)
	}
	if len(host) > 253 {
		return "", fmt.Errorf("%w: host is too long", ErrInvalidTarget)
	}
	if strings.Contains(host, "..") {
		return "", fmt.Errorf("%w: empty label", ErrInvalidTarget)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return "", fmt.Errorf("%w: empty label", ErrInvalidTarget)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("%w: label is too long", ErrInvalidTarget)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%w: label starts or ends with '-'", ErrInvalidTarget)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", fmt.Errorf("%w: invalid character in hostname", ErrInvalidTarget)
			}
		}
	}
	return host, nil
}
