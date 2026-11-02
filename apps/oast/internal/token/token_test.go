package token

import (
	"testing"
)

func TestCryptoGeneratorFormatAndUniqueness(t *testing.T) {
	g := CryptoGenerator{}
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		got, err := g.New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if !IsValid(got) {
			t.Fatalf("New() = %q, not a valid token", got)
		}
		if len(got) != EncodedLength {
			t.Fatalf("len(New()) = %d, want %d", len(got), EncodedLength)
		}
		if _, exists := seen[got]; exists {
			t.Fatalf("duplicate token %q", got)
		}
		seen[got] = struct{}{}
	}
}

func TestIsValid(t *testing.T) {
	const valid = "abcdefghijklmnopqrstuvwxyz" // 26 lower-case base32 characters
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"valid lower base32", valid, true},
		{"too short", valid[:25], false},
		{"too long", valid + "2", false},
		{"uppercase", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", false},
		{"invalid char 0", "0bcdefghijklmnopqrstuvwxyz", false},
		{"invalid char 8", "8bcdefghijklmnopqrstuvwxyz", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValid(tt.in); got != tt.want {
				t.Fatalf("IsValid(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
