package evidence

import (
	"testing"
	"time"
)

func TestHashBody(t *testing.T) {
	got := HashBody([]byte("hello"))
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("HashBody() = %q, want %q", got, want)
	}
}

func TestSizeAndClone(t *testing.T) {
	original := Record{
		TestID:     "test",
		Sequence:   1,
		Protocol:   ProtocolHTTP,
		ObservedAt: time.Unix(100, 0).UTC(),
		SourceIP:   "192.0.2.1",
		Assessment: AssessmentObservedOnly,
		HTTP: &HTTPInteraction{
			Method:  "POST",
			Headers: map[string][]string{"X-Test": {"one"}},
			Query:   map[string][]string{"a": {"1"}},
			Body:    []byte("body"),
		},
	}
	if Size(original) <= 0 {
		t.Fatal("Size() must be positive")
	}
	clone := original.Clone()
	clone.HTTP.Headers["X-Test"][0] = "changed"
	clone.HTTP.Query["a"][0] = "changed"
	clone.HTTP.Body[0] = 'X'
	if original.HTTP.Headers["X-Test"][0] != "one" {
		t.Fatal("Clone shared headers")
	}
	if original.HTTP.Query["a"][0] != "1" {
		t.Fatal("Clone shared query")
	}
	if string(original.HTTP.Body) != "body" {
		t.Fatal("Clone shared body")
	}
}
