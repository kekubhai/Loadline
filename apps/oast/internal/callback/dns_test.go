package callback

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/ratelimit"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

type recordingWriter struct {
	msg    *dns.Msg
	remote net.Addr
}

func (w *recordingWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}
}
func (w *recordingWriter) RemoteAddr() net.Addr { return w.remote }
func (w *recordingWriter) WriteMsg(m *dns.Msg) error {
	w.msg = m.Copy()
	return nil
}
func (w *recordingWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *recordingWriter) Close() error                { return nil }
func (w *recordingWriter) TsigStatus() error           { return nil }
func (w *recordingWriter) TsigTimersOnly(bool)         {}
func (w *recordingWriter) Hijack()                     {}

func newDNSStore(t *testing.T) (*registry.Memory, *clock.Manual) {
	t.Helper()
	clk := clock.NewManual(time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC))
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      50,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clk, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	return store, clk
}

func newDNSTest(t *testing.T, store *registry.Memory, limiter *ratelimit.Limiter) *DNSHandler {
	t.Helper()
	h, err := NewDNSHandler(DNSConfig{
		Zone: testDomain,
		IPv4: net.ParseIP("192.0.2.44"),
		IPv6: net.ParseIP("2001:db8::44"),
		TTL:  30,
	}, store, limiter, nil)
	if err != nil {
		t.Fatalf("NewDNSHandler() error = %v", err)
	}
	return h
}

func query(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

func serveQuery(t *testing.T, h *DNSHandler, m *dns.Msg) (*recordingWriter, *dns.Msg) {
	t.Helper()
	w := &recordingWriter{remote: &net.UDPAddr{IP: net.ParseIP("198.51.100.4"), Port: 33333}}
	h.ServeDNS(w, m)
	if w.msg == nil {
		t.Fatal("handler wrote no response")
	}
	return w, w.msg
}

func TestDNSValidAAndEvidence(t *testing.T) {
	store, _ := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newDNSTest(t, store, nil)

	_, msg := serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Rcode = %s", dns.RcodeToString[msg.Rcode])
	}
	if !msg.Authoritative || msg.RecursionAvailable {
		t.Fatalf("authoritative=%v recursionAvailable=%v", msg.Authoritative, msg.RecursionAvailable)
	}
	if len(msg.Answer) != 1 {
		t.Fatalf("answer count = %d, want 1", len(msg.Answer))
	}
	a, ok := msg.Answer[0].(*dns.A)
	if !ok || a.A.String() != "192.0.2.44" {
		t.Fatalf("answer = %#v", msg.Answer[0])
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence count = %d", len(got.Evidence))
	}
	rec := got.Evidence[0]
	if rec.Protocol != "dns" || rec.Assessment != "observed_interaction_only" {
		t.Fatalf("record = %#v", rec)
	}
	if rec.SourceIP != "198.51.100.4" {
		t.Fatalf("SourceIP = %q", rec.SourceIP)
	}
	if rec.DNS == nil || rec.DNS.Type != dns.TypeA || rec.DNS.TypeName != "A" {
		t.Fatalf("DNS metadata = %#v", rec.DNS)
	}
	if rec.DNS.Class != dns.ClassINET || rec.DNS.ClassName != "IN" {
		t.Fatalf("DNS class = %#v", rec.DNS)
	}
}

func TestDNSAAAAAndAny(t *testing.T) {
	store, _ := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newDNSTest(t, store, nil)

	_, msg := serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeAAAA))
	if len(msg.Answer) != 1 {
		t.Fatalf("AAAA answer count = %d", len(msg.Answer))
	}
	if aaaa, ok := msg.Answer[0].(*dns.AAAA); !ok || aaaa.AAAA.String() != "2001:db8::44" {
		t.Fatalf("AAAA answer = %#v", msg.Answer[0])
	}

	_, msg = serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeANY))
	if len(msg.Answer) != 2 {
		t.Fatalf("ANY answer count = %d, want 2", len(msg.Answer))
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(got.Evidence))
	}
}

func TestDNSTXTIsRecordedWithoutAnswer(t *testing.T) {
	store, _ := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newDNSTest(t, store, nil)

	_, msg := serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeTXT))
	if msg.Rcode != dns.RcodeSuccess || len(msg.Answer) != 0 {
		t.Fatalf("TXT response = rcode %d answers %d", msg.Rcode, len(msg.Answer))
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].DNS.Type != dns.TypeTXT {
		t.Fatalf("evidence = %#v", got.Evidence)
	}
}

func TestDNSUnknownClosedExpiredAndInvalid(t *testing.T) {
	store, clk := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newDNSTest(t, store, nil)

	unknown, err := token.CryptoGenerator{}.New()
	if err != nil {
		t.Fatalf("token generation error = %v", err)
	}
	_, msg := serveQuery(t, h, query(unknown+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeNameError {
		t.Fatalf("unknown Rcode = %s, want NXDOMAIN", dns.RcodeToString[msg.Rcode])
	}

	names := []string{
		testDomain,
		"a." + created.Token + "." + testDomain,
		"not-a-token." + testDomain,
	}
	for _, name := range names {
		_, msg = serveQuery(t, h, query(name, dns.TypeA))
		if msg.Rcode != dns.RcodeNameError {
			t.Fatalf("%s Rcode = %s, want NXDOMAIN", name, dns.RcodeToString[msg.Rcode])
		}
	}

	_, msg = serveQuery(t, h, query(created.Token+".outside.test", dns.TypeA))
	if msg.Rcode != dns.RcodeRefused {
		t.Fatalf("outside-zone Rcode = %s, want REFUSED", dns.RcodeToString[msg.Rcode])
	}

	if err := store.Close(context.Background(), created.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, msg = serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeNameError {
		t.Fatalf("closed Rcode = %s, want NXDOMAIN", dns.RcodeToString[msg.Rcode])
	}

	expired := createCallbackTest(t, store, time.Minute)
	clk.Advance(2 * time.Minute)
	_, msg = serveQuery(t, h, query(expired.Token+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeNameError {
		t.Fatalf("expired Rcode = %s, want NXDOMAIN", dns.RcodeToString[msg.Rcode])
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("invalid queries created %d evidence records", len(got.Evidence))
	}
}

func TestDNSRejectsUnsupportedOperationsAndMalformedQueries(t *testing.T) {
	store, _ := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newDNSTest(t, store, nil)

	axfr := query(created.Token+"."+testDomain, dns.TypeAXFR)
	_, msg := serveQuery(t, h, axfr)
	if msg.Rcode != dns.RcodeRefused {
		t.Fatalf("AXFR Rcode = %s", dns.RcodeToString[msg.Rcode])
	}

	chaos := query(created.Token+"."+testDomain, dns.TypeA)
	chaos.Question[0].Qclass = dns.ClassCHAOS
	_, msg = serveQuery(t, h, chaos)
	if msg.Rcode != dns.RcodeRefused {
		t.Fatalf("CHAOS Rcode = %s", dns.RcodeToString[msg.Rcode])
	}

	multi := query(created.Token+"."+testDomain, dns.TypeA)
	multi.Question = append(multi.Question, multi.Question[0])
	_, msg = serveQuery(t, h, multi)
	if msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("multi-question Rcode = %s", dns.RcodeToString[msg.Rcode])
	}

	empty := new(dns.Msg)
	empty.Id = 1234
	_, msg = serveQuery(t, h, empty)
	if msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("empty-question Rcode = %s", dns.RcodeToString[msg.Rcode])
	}

	long := query(strings.Repeat("a", 300)+"."+testDomain, dns.TypeA)
	_, msg = serveQuery(t, h, long)
	if msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("long-name Rcode = %s", dns.RcodeToString[msg.Rcode])
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("unsupported queries created %d evidence records", len(got.Evidence))
	}

	// Nil requests must not panic or write anything.
	w := &recordingWriter{remote: &net.UDPAddr{IP: net.ParseIP("198.51.100.4"), Port: 1}}
	h.ServeDNS(w, nil)
	if w.msg != nil {
		t.Fatal("nil request produced a response")
	}
}

func TestDNSRateLimit(t *testing.T) {
	store, _ := newDNSStore(t)
	created := createCallbackTest(t, store, time.Minute)
	limiter := ratelimit.New(0.001, 1, clock.NewManual(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
	h := newDNSTest(t, store, limiter)

	_, msg := serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("first Rcode = %s", dns.RcodeToString[msg.Rcode])
	}
	_, msg = serveQuery(t, h, query(created.Token+"."+testDomain, dns.TypeA))
	if msg.Rcode != dns.RcodeRefused {
		t.Fatalf("second Rcode = %s, want REFUSED", dns.RcodeToString[msg.Rcode])
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence count = %d", len(got.Evidence))
	}
}

func TestDNSConstructorValidation(t *testing.T) {
	store, _ := newDNSStore(t)
	tests := []DNSConfig{
		{Zone: "", IPv4: net.ParseIP("192.0.2.1")},
		{Zone: "127.0.0.1", IPv4: net.ParseIP("192.0.2.1")},
		{Zone: testDomain},
		{Zone: testDomain, IPv4: net.ParseIP("::1")},
		{Zone: testDomain, IPv6: net.ParseIP("192.0.2.1")},
	}
	for _, cfg := range tests {
		if _, err := NewDNSHandler(cfg, store, nil, nil); err == nil {
			t.Fatalf("NewDNSHandler(%#v) error = nil", cfg)
		}
	}
	if _, err := NewDNSHandler(DNSConfig{Zone: testDomain, IPv4: net.ParseIP("192.0.2.1")}, nil, nil, nil); err == nil {
		t.Fatal("nil sink must be rejected")
	}
}
