package edgetopology

// adr: 705

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServedDNSWireRejectsMismatchedMalformedAndOversizedFrames(t *testing.T) {
	for _, mode := range []string{"id", "question-name", "question-type", "question-class", "not-response", "opcode", "rcode", "truncated", "recursive", "reserved", "missing-question", "multiple-questions", "lying-count", "rr-bound", "trailing", "compression-cycle", "oversized", "short"} {
		t.Run(mode, func(t *testing.T) {
			address := tcpDNSFixture(t, func(q *dns.Msg) []byte {
				r := new(dns.Msg)
				r.SetReply(q)
				r.Authoritative = true
				r.Answer = []dns.RR{&dns.A{Hdr: dnsHeader("origin.gregale.dev", dns.TypeA), A: net.ParseIP("192.0.2.10")}}
				switch mode {
				case "id":
					r.Id++
				case "question-name":
					r.Question[0].Name = "elsewhere.gregale.dev."
				case "question-type":
					r.Question[0].Qtype = dns.TypeAAAA
				case "question-class":
					r.Question[0].Qclass = dns.ClassCHAOS
				case "not-response":
					r.Response = false
				case "opcode":
					r.Opcode = dns.OpcodeUpdate
				case "rcode":
					r.Rcode = dns.RcodeServerFailure
				case "truncated":
					r.Truncated = true
				case "recursive":
					r.RecursionDesired = true
				case "reserved":
					r.Zero = true
				case "missing-question":
					r.Question = nil
				case "multiple-questions":
					r.Question = append(r.Question, r.Question[0])
				case "rr-bound":
					for len(r.Answer) <= api.RuntimeUpgradeServedDNSRRLimit {
						r.Answer = append(r.Answer, r.Answer[0])
					}
				}
				body, err := r.Pack()
				if err != nil {
					t.Error(err)
				}
				switch mode {
				case "lying-count":
					binary.BigEndian.PutUint16(body[6:8], 2)
				case "trailing":
					body = append(body, 0)
				case "compression-cycle":
					body[12], body[13] = 0xc0, 12
				case "oversized":
					body = make([]byte, api.RuntimeUpgradeServedDNSWireMaxBytes+1)
				case "short":
					body = body[:11]
				}
				return body
			})
			got, digest, err := exchangeServedDNS(t.Context(), address, DNSQuestion{Name: "origin.gregale.dev", Type: "A"})
			if !errors.Is(err, ErrDNSUnverified) || got != nil || digest != "" {
				t.Fatal("malformed wire frame accepted", got, digest, err)
			}
		})
	}
}

func TestServedDNSWireAcceptsBoundedCompressedRepliesWithExactQuestion(t *testing.T) {
	address := tcpDNSFixture(t, func(q *dns.Msg) []byte {
		r := new(dns.Msg)
		r.SetReply(q)
		r.Authoritative = true
		r.Compress = true
		r.Answer = []dns.RR{&dns.A{Hdr: dnsHeader("origin.gregale.dev", dns.TypeA), A: net.ParseIP("192.0.2.10")}, &dns.A{Hdr: dnsHeader("origin.gregale.dev", dns.TypeA), A: net.ParseIP("192.0.2.11")}}
		body, err := r.Pack()
		if err != nil {
			t.Error(err)
		}
		return body
	})
	got, digest, err := exchangeServedDNS(t.Context(), address, DNSQuestion{Name: "origin.gregale.dev", Type: "A"})
	if err != nil || len(got.Answer) != 2 || !canonicalDigest(digest) {
		t.Fatal(got, digest, err)
	}
}

func TestServedDNSWireRequiresCanonicalLiteralUnicastEndpoints(t *testing.T) {
	for _, address := range []string{"127.0.0.1:53", "[::1]:1053", "192.0.2.10:53", "[2001:db8::10]:53"} {
		if !dnsEndpoint(address) {
			t.Fatal("canonical endpoint rejected", address)
		}
	}
	for _, address := range []string{"ns.example.net:53", ":53", "127.0.0.1:0", "127.0.0.1:053", "0.0.0.0:53", "[::]:53", "224.0.0.1:53", "[ff02::1]:53", "[::ffff:127.0.0.1]:53", "[fe80::1%lo0]:53", "127.0.0.1:65536", "tcp/127.0.0.1:53"} {
		if dnsEndpoint(address) {
			t.Fatal("ambiguous endpoint accepted", address)
		}
	}
}
