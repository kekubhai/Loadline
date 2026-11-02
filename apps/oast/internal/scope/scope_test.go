package scope

import (
	"errors"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Example.COM.", "example.com", false},
		{"a-b.example", "a-b.example", false},
		{"localhost", "localhost", false},
		{"bad_host.example", "", true},
		{"-bad.example", "", true},
		{"bad-.example", "", true},
		{"a..b", "", true},
		{"", "", true},
		{"a/b", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeHostname(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeHostname(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeHostname(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantHost string
		wantPort string
		wantIP   string
		wantErr  bool
	}{
		{"url", "https://Lab.Example.com:8443/a?b=1", "lab.example.com", "8443", "", false},
		{"host", "Lab.Example.com.", "lab.example.com", "", "", false},
		{"host port", "lab.example.com:8080", "lab.example.com", "8080", "", false},
		{"ipv4", "192.0.2.10", "192.0.2.10", "", "192.0.2.10", false},
		{"ipv4 port", "192.0.2.10:8080", "192.0.2.10", "8080", "192.0.2.10", false},
		{"ipv6", "2001:db8::1", "2001:db8::1", "", "2001:db8::1", false},
		{"ipv6 bracket port", "[2001:db8::1]:8080", "2001:db8::1", "8080", "2001:db8::1", false},
		{"empty", "", "", "", "", true},
		{"bad scheme", "ftp://example.com", "", "", "", true},
		{"userinfo", "https://user@example.com", "", "", "", true},
		{"path without scheme", "example.com/path", "", "", "", true},
		{"bad port", "example.com:99999", "", "", "", true},
		{"bad host", "exa mple.com", "", "", "", true},
		{"control", "example.com\x00", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTarget(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseTarget(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Host != tt.wantHost || got.Port != tt.wantPort {
				t.Fatalf("ParseTarget(%q) = host %q port %q, want %q %q", tt.in, got.Host, got.Port, tt.wantHost, tt.wantPort)
			}
			if tt.wantIP == "" {
				if got.IP != nil {
					t.Fatalf("ParseTarget(%q) IP = %v, want nil", tt.in, got.IP)
				}
			} else if got.IP == nil || got.IP.String() != tt.wantIP {
				t.Fatalf("ParseTarget(%q) IP = %v, want %s", tt.in, got.IP, tt.wantIP)
			}
		})
	}
}

func TestValidatorMatches(t *testing.T) {
	v, err := New([]string{
		"example.com",
		"*.apps.example.com",
		"192.0.2.10",
		"10.0.0.0/8",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		target string
		want   bool
	}{
		{"example.com", true},
		{"EXAMPLE.COM.", true},
		{"api.example.com", false},
		{"one.apps.example.com", true},
		{"one.two.apps.example.com", true},
		{"apps.example.com", false},
		{"192.0.2.10", true},
		{"192.0.2.11", false},
		{"10.4.5.6", true},
		{"11.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			target, err := ParseTarget(tt.target)
			if err != nil {
				t.Fatalf("ParseTarget(%q) error = %v", tt.target, err)
			}
			if got := v.Matches(target); got != tt.want {
				t.Fatalf("Matches(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}

	if _, err := v.Authorize("evil.example.net"); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("Authorize() error = %v, want ErrOutOfScope", err)
	}
	if _, err := v.Authorize("not a target"); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("Authorize() error = %v, want ErrInvalidTarget", err)
	}
}

func TestNewFailsClosedAndRejectsBadPatterns(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNoScopes) {
		t.Fatalf("New(nil) error = %v, want ErrNoScopes", err)
	}
	if _, err := New([]string{""}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("New(empty) error = %v, want ErrInvalidScope", err)
	}
	for _, pattern := range []string{"https://example.com", "*", "a.*.example.com", "bad_host"} {
		if _, err := New([]string{pattern}); !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("New(%q) error = %v, want ErrInvalidScope", pattern, err)
		}
	}
}
