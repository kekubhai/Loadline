// Package token generates unpredictable callback identifiers.
//
// Tokens are 128 bits from crypto/rand, encoded as lower-case unpadded
// base32. That yields a 26-character DNS label using only [a-z2-7].
package token

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

const (
	// EntropyBytes is the amount of randomness in a callback token.
	EntropyBytes = 16
	// EncodedLength is the fixed length of a base32-encoded token.
	EncodedLength = 26
)

var base32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Generator produces callback tokens. Tests can inject a deterministic
// implementation; production uses CryptoGenerator.
type Generator interface {
	New() (string, error)
}

// CryptoGenerator uses crypto/rand.
type CryptoGenerator struct{}

// New implements Generator.
func (CryptoGenerator) New() (string, error) {
	buf := make([]byte, EntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return strings.ToLower(base32Encoding.EncodeToString(buf)), nil
}

// Func adapts a function to Generator.
type Func func() (string, error)

// New implements Generator.
func (f Func) New() (string, error) { return f() }

// IsValid reports whether s has the exact shape of a generated token.
// This is a cheap shape check, not a substitute for registry lookup.
func IsValid(s string) bool {
	if len(s) != EncodedLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}
