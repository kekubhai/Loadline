package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireLoopbackFailsClosed(t *testing.T) {
	ok := []string{"127.0.0.1:8099", "localhost:0", "[::1]:9000"}
	for _, a := range ok {
		if err := requireLoopback(a); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"0.0.0.0:8099", "192.168.1.5:8099", "example.com:8099", "nothostport"}
	for _, a := range bad {
		if err := requireLoopback(a); err == nil {
			t.Errorf("requireLoopback(%q) = nil, want error (must refuse non-loopback)", a)
		}
	}
}

func TestSplitScopes(t *testing.T) {
	got := splitScopes(" lab.local , *.apps.lab.test ,, ")
	if len(got) != 2 || got[0] != "lab.local" || got[1] != "*.apps.lab.test" {
		t.Fatalf("splitScopes = %#v", got)
	}
}

func TestPageServesHTML(t *testing.T) {
	c := &console{}
	rec := httptest.NewRecorder()
	c.page(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>OAST Console</title>") {
		t.Fatalf("page did not render the console HTML")
	}
	// The safety framing must be present in the UI.
	if !strings.Contains(body, "not proof of a vulnerability") {
		t.Fatalf("page is missing the observed-interaction disclaimer")
	}
}
