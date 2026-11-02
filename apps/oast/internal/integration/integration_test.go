package integration

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/kekubhai/Loadline/apps/oast/internal/callback"
	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

const (
	domain       = "oast.local"
	callbackHost = "127.0.0.1"
)

func TestHTTPAndDNSLifecycle(t *testing.T) {
	clk := clock.NewManual(time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC))
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      50,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clk, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, err := scope.New([]string{"lab.local"})
	if err != nil {
		t.Fatalf("scope.New() error = %v", err)
	}
	svc, err := service.New(store, scopes, service.Config{
		DefaultTTL: 10 * time.Minute,
		MaxTTL:     time.Hour,
	})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	created, err := svc.CreateTest(context.Background(), service.CreateRequest{
		Target:           "http://lab.local/checkout",
		AuthorizationRef: "engagement-e2e",
	})
	if err != nil {
		t.Fatalf("CreateTest() error = %v", err)
	}

	httpHandler, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain:   domain,
		MaxBodyBytes:   4096,
		MaxHeaderBytes: 4096,
		MaxQueryBytes:  512,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	httpSrv := httptest.NewServer(httpHandler)
	defer httpSrv.Close()

	httpClient := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/callback?source=http&value=1", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Host = created.Token + "." + domain
	req.Header.Set("Authorization", "Bearer e2e-secret")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP callback error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP callback status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Location") != "" {
		t.Fatalf("HTTP callback returned dynamic headers: %#v", resp.Header)
	}

	dnsHandler, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: domain,
		IPv4: net.ParseIP(callbackHost),
		IPv6: net.ParseIP("::1"),
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewDNSHandler() error = %v", err)
	}
	udpConn, err := net.ListenPacket("udp", net.JoinHostPort(callbackHost, "0"))
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer udpConn.Close()
	port := udpConn.LocalAddr().(*net.UDPAddr).Port
	tcpListener, err := net.Listen("tcp", net.JoinHostPort(callbackHost, strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer tcpListener.Close()

	udpServer := &dns.Server{PacketConn: udpConn, Handler: dnsHandler}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: dnsHandler}
	go func() { _ = udpServer.ActivateAndServe() }()
	go func() { _ = tcpServer.ActivateAndServe() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = udpServer.ShutdownContext(ctx)
		_ = tcpServer.ShutdownContext(ctx)
	}()

	udpClient := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	udpResp, _, err := udpClient.Exchange(newAQuery(created.Token+"."+domain), udpConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("UDP DNS query error = %v", err)
	}
	if udpResp.Rcode != dns.RcodeSuccess || len(udpResp.Answer) != 1 {
		t.Fatalf("UDP DNS response = rcode %d answers %d", udpResp.Rcode, len(udpResp.Answer))
	}

	tcpClient := &dns.Client{Net: "tcp", Timeout: 2 * time.Second}
	tcpResp, _, err := tcpClient.Exchange(newAAAAQuery(created.Token+"."+domain), tcpListener.Addr().String())
	if err != nil {
		t.Fatalf("TCP DNS query error = %v", err)
	}
	if tcpResp.Rcode != dns.RcodeSuccess || len(tcpResp.Answer) != 1 {
		t.Fatalf("TCP DNS response = rcode %d answers %d", tcpResp.Rcode, len(tcpResp.Answer))
	}

	got, err := svc.GetTest(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTest() error = %v", err)
	}
	if len(got.Evidence) != 3 {
		t.Fatalf("evidence count = %d, want 3", len(got.Evidence))
	}
	var httpCount, dnsCount int
	for _, rec := range got.Evidence {
		if rec.Assessment != evidence.AssessmentObservedOnly {
			t.Fatalf("assessment = %q", rec.Assessment)
		}
		switch rec.Protocol {
		case evidence.ProtocolHTTP:
			httpCount++
		case evidence.ProtocolDNS:
			dnsCount++
		}
	}
	if httpCount != 1 || dnsCount != 2 {
		t.Fatalf("evidence mix = http %d dns %d", httpCount, dnsCount)
	}

	if err := svc.CloseTest(context.Background(), created.ID); err != nil {
		t.Fatalf("CloseTest() error = %v", err)
	}

	req, err = http.NewRequest(http.MethodGet, httpSrv.URL+"/after-close", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Host = created.Token + "." + domain
	resp, err = httpClient.Do(req)
	if err != nil {
		t.Fatalf("closed HTTP callback error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("closed HTTP status = %d, want 404", resp.StatusCode)
	}

	closedResp, _, err := udpClient.Exchange(newAQuery(created.Token+"."+domain), udpConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("closed DNS query error = %v", err)
	}
	if closedResp.Rcode != dns.RcodeNameError {
		t.Fatalf("closed DNS Rcode = %s", dns.RcodeToString[closedResp.Rcode])
	}

	got, err = svc.GetTest(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTest() after close error = %v", err)
	}
	if len(got.Evidence) != 3 {
		t.Fatalf("closed test accepted new evidence: %d records", len(got.Evidence))
	}
	if got.State != registry.StateClosed {
		t.Fatalf("state = %s, want closed", got.State)
	}
}

func newAQuery(name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	return m
}

func newAAAAQuery(name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeAAAA)
	return m
}
