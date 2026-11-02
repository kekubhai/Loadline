package callback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/miekg/dns"

	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/ratelimit"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

// DNSConfig controls the authoritative callback zone.
type DNSConfig struct {
	Zone string
	IPv4 net.IP
	IPv6 net.IP
	TTL  uint32
}

// DNSHandler answers only for the configured callback zone.
type DNSHandler struct {
	cfg     DNSConfig
	sink    registry.EvidenceSink
	limiter *ratelimit.Limiter
	logger  *slog.Logger
}

// NewDNSHandler validates cfg and returns an authoritative dns.Handler.
func NewDNSHandler(cfg DNSConfig, sink registry.EvidenceSink, limiter *ratelimit.Limiter, logger *slog.Logger) (*DNSHandler, error) {
	if sink == nil {
		return nil, fmt.Errorf("dns callback: evidence sink is required")
	}
	zone, err := scope.NormalizeHostname(cfg.Zone)
	if err != nil {
		return nil, fmt.Errorf("dns callback: zone: %w", err)
	}
	if net.ParseIP(zone) != nil {
		return nil, fmt.Errorf("dns callback: zone must be a DNS name")
	}
	if cfg.IPv4 != nil {
		ip4 := cfg.IPv4.To4()
		if ip4 == nil {
			return nil, fmt.Errorf("dns callback: IPv4 must be an IPv4 address")
		}
		cfg.IPv4 = ip4
	}
	if cfg.IPv6 != nil {
		if cfg.IPv6.To4() != nil || cfg.IPv6.To16() == nil {
			return nil, fmt.Errorf("dns callback: IPv6 must be an IPv6 address")
		}
		cfg.IPv6 = cfg.IPv6.To16()
	}
	if cfg.IPv4 == nil && cfg.IPv6 == nil {
		return nil, fmt.Errorf("dns callback: at least one answer address is required")
	}
	if cfg.TTL == 0 {
		cfg.TTL = 30
	}
	cfg.Zone = zone
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &DNSHandler{cfg: cfg, sink: sink, limiter: limiter, logger: logger}, nil
}

// ServeDNS implements dns.Handler.
func (h *DNSHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if req == nil {
		return
	}
	if h.limiter != nil && !h.limiter.Allow() {
		h.write(w, h.errorReply(req, dns.RcodeRefused))
		return
	}
	if req.Opcode != dns.OpcodeQuery {
		h.write(w, h.errorReply(req, dns.RcodeRefused))
		return
	}
	if len(req.Question) != 1 {
		h.write(w, h.errorReply(req, dns.RcodeFormatError))
		return
	}

	q := req.Question[0]
	if q.Qtype == dns.TypeAXFR || q.Qtype == dns.TypeIXFR || q.Qclass != dns.ClassINET {
		h.write(w, h.errorReply(req, dns.RcodeRefused))
		return
	}

	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	if name == "" || len(name) > 253 {
		h.write(w, h.errorReply(req, dns.RcodeFormatError))
		return
	}

	callbackToken, inZone := extractDNSToken(name, h.cfg.Zone)
	if !inZone {
		h.write(w, h.errorReply(req, dns.RcodeRefused))
		return
	}
	if callbackToken == "" || !token.IsValid(callbackToken) {
		h.write(w, h.errorReply(req, dns.RcodeNameError))
		return
	}

	record := evidence.Record{
		Protocol: evidence.ProtocolDNS,
		SourceIP: sourceIP(w.RemoteAddr()),
		DNS: &evidence.DNSInteraction{
			Name:      q.Name,
			Type:      q.Qtype,
			TypeName:  dnsTypeName(q.Qtype),
			Class:     q.Qclass,
			ClassName: dnsClassName(q.Qclass),
		},
	}

	_, appendErr := h.sink.AppendEvidence(context.Background(), callbackToken, record)
	switch {
	case appendErr == nil:
		h.write(w, h.answer(req, q))
	case errors.Is(appendErr, registry.ErrNotFound),
		errors.Is(appendErr, registry.ErrClosed),
		errors.Is(appendErr, registry.ErrExpired):
		h.write(w, h.errorReply(req, dns.RcodeNameError))
	case errors.Is(appendErr, registry.ErrEvidenceLimit),
		errors.Is(appendErr, registry.ErrInvalidEvidence):
		// A valid active test that has exhausted its evidence budget
		// still gets a normal answer; the record is dropped.
		h.write(w, h.answer(req, q))
	default:
		h.logger.Warn("dns callback evidence not recorded", "error", appendErr)
		h.write(w, h.errorReply(req, dns.RcodeServerFailure))
	}
}

func extractDNSToken(name, zone string) (callbackToken string, inZone bool) {
	if name == zone {
		return "", true
	}
	suffix := "." + zone
	if !strings.HasSuffix(name, suffix) {
		return "", false
	}
	label := strings.TrimSuffix(name, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", true
	}
	return label, true
}

func (h *DNSHandler) answer(req *dns.Msg, q dns.Question) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = false

	addA := q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY
	addAAAA := q.Qtype == dns.TypeAAAA || q.Qtype == dns.TypeANY
	if addA && h.cfg.IPv4 != nil {
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    h.cfg.TTL,
			},
			A: append(net.IP(nil), h.cfg.IPv4...),
		})
	}
	if addAAAA && h.cfg.IPv6 != nil {
		m.Answer = append(m.Answer, &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    h.cfg.TTL,
			},
			AAAA: append(net.IP(nil), h.cfg.IPv6...),
		})
	}
	return m
}

func (h *DNSHandler) errorReply(req *dns.Msg, rcode int) *dns.Msg {
	m := new(dns.Msg)
	m.SetRcode(req, rcode)
	m.Authoritative = true
	m.RecursionAvailable = false
	return m
}

func (h *DNSHandler) write(w dns.ResponseWriter, msg *dns.Msg) {
	if w == nil {
		return
	}
	if err := w.WriteMsg(msg); err != nil {
		h.logger.Debug("dns response write failed", "error", err)
	}
}

func sourceIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host
	}
	return strings.TrimSpace(addr.String())
}

func dnsTypeName(t uint16) string {
	if name, ok := dns.TypeToString[t]; ok {
		return name
	}
	return fmt.Sprintf("TYPE%d", t)
}

func dnsClassName(c uint16) string {
	if name, ok := dns.ClassToString[c]; ok {
		return name
	}
	return fmt.Sprintf("CLASS%d", c)
}
