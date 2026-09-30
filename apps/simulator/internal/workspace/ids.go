package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// NewID returns a random RFC 4122 version 4 UUID in canonical lowercase
// form (8-4-4-4-12).
//
// Identifiers are generated in Go rather than by the database default so a
// caller can create a parent and its child in one transaction without an
// extra round trip to learn the parent's id. The database default remains
// as a safety net for rows inserted by hand.
//
// IDs are not part of any simulation input: they never influence a
// simulation result, so their randomness cannot affect determinism.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the process cannot produce identifiers
		// at all; there is no safe fallback (a predictable id would be a
		// collision risk), so fail loudly.
		panic(fmt.Sprintf("workspace: cannot read random bytes for an id: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return formatUUID(b)
}

// ValidateID reports whether id is a canonical UUID. It returns ErrInvalidID
// otherwise, so malformed input is rejected in Go before it reaches a
// PostgreSQL uuid cast.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return fmt.Errorf("%w: %q is not a canonical UUID", ErrInvalidID, id)
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isHexDigit(byte(c)) {
			return fmt.Errorf("%w: %q is not a canonical UUID", ErrInvalidID, id)
		}
	}
	return nil
}

// IsNotFound reports whether err means "no such document".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsInvalidID reports whether err means "malformed identifier".
func IsInvalidID(err error) bool { return errors.Is(err, ErrInvalidID) }

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// formatUUID renders 16 bytes as a canonical hyphenated UUID.
func formatUUID(b [16]byte) string {
	var sb strings.Builder
	sb.Grow(36)
	enc := hex.EncodeToString(b[:])
	for i, c := range enc {
		if i == 8 || i == 12 || i == 16 || i == 20 {
			sb.WriteByte('-')
		}
		sb.WriteByte(byte(c))
	}
	return sb.String()
}
