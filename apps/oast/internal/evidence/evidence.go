// Package evidence defines the structured records the OAST engine stores.
//
// An observed callback is evidence that a network interaction reached the
// listener. It is not, by itself, proof of SSRF, unauthorized access, or
// impact. Every record therefore carries AssessmentObservedOnly.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Protocol identifies the interaction channel.
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolDNS  Protocol = "dns"
)

// AssessmentObservedOnly is the only assessment this service emits.
const AssessmentObservedOnly = "observed_interaction_only"

// Record is one observed interaction belonging to a test.
type Record struct {
	TestID     string           `json:"test_id"`
	Sequence   uint64           `json:"sequence"`
	EvidenceID string           `json:"evidence_id"`
	Protocol   Protocol         `json:"protocol"`
	ObservedAt time.Time        `json:"observed_at"`
	SourceIP   string           `json:"source_ip"`
	Assessment string           `json:"assessment"`
	HTTP       *HTTPInteraction `json:"http,omitempty"`
	DNS        *DNSInteraction  `json:"dns,omitempty"`
}

// HTTPInteraction is the HTTP-specific portion of a record.
type HTTPInteraction struct {
	Method                string              `json:"method"`
	Host                  string              `json:"host"`
	RawHost               string              `json:"raw_host"`
	Path                  string              `json:"path"`
	RawQuery              string              `json:"raw_query,omitempty"`
	Query                 map[string][]string `json:"query,omitempty"`
	QueryTruncated        bool                `json:"query_truncated,omitempty"`
	Headers               map[string][]string `json:"headers,omitempty"`
	HeadersRedacted       bool                `json:"headers_redacted,omitempty"`
	HeadersTruncated      bool                `json:"headers_truncated,omitempty"`
	ContentType           string              `json:"content_type,omitempty"`
	Body                  []byte              `json:"body,omitempty"`
	BodySHA256            string              `json:"body_sha256"`
	BodyCapturedBytes     int                 `json:"body_captured_bytes"`
	DeclaredContentLength int64               `json:"declared_content_length"`
	BodyTruncated         bool                `json:"body_truncated,omitempty"`
}

// DNSInteraction is the DNS-specific portion of a record.
type DNSInteraction struct {
	Name      string `json:"name"`
	Type      uint16 `json:"type"`
	TypeName  string `json:"type_name"`
	Class     uint16 `json:"class"`
	ClassName string `json:"class_name"`
}

// HashBody returns the lower-case hex SHA-256 of b.
func HashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Size returns the approximate in-memory budget consumed by the JSON
// representation of r. Registry limits are enforced against this value.
func Size(r Record) int64 {
	b, err := json.Marshal(r)
	if err != nil {
		// All fields in Record are JSON-serializable. If that ever
		// changes, charging the maximum is the fail-closed choice.
		return int64(^uint64(0) >> 1)
	}
	return int64(len(b))
}

// Clone returns a deep copy so callers cannot mutate registry state.
func (r Record) Clone() Record {
	out := r
	if r.HTTP != nil {
		h := *r.HTTP
		h.Query = cloneValues(h.Query)
		h.Headers = cloneValues(h.Headers)
		if r.HTTP.Body != nil {
			h.Body = append([]byte(nil), r.HTTP.Body...)
		}
		out.HTTP = &h
	}
	if r.DNS != nil {
		d := *r.DNS
		out.DNS = &d
	}
	return out
}

func cloneValues(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, values := range in {
		out[k] = append([]string(nil), values...)
	}
	return out
}
