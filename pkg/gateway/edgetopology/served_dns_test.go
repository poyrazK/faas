package edgetopology

// adr: 705

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

type servedDNSFixture struct {
	probe     *CloudflareDNSProbe
	review    ServedDNSReview
	apiCalls  atomic.Int32
	dnsCalls  atomic.Int32
	changeAPI atomic.Bool
	mutate    func(parent bool, reply *dns.Msg)
	started   chan struct{}
	release   chan struct{}
	gate      atomic.Bool
}

func tcpDNSFixture(t *testing.T, handler func(*dns.Msg) []byte) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	closed := false
	connections := make(map[net.Conn]bool)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				_ = conn.Close()
				mu.Unlock()
				continue
			}
			connections[conn] = true
			mu.Unlock()
			group.Add(1)
			go func() {
				defer group.Done()
				defer func() { _ = conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				body := make([]byte, int(binary.BigEndian.Uint16(size[:])))
				if _, err := io.ReadFull(conn, body); err != nil {
					return
				}
				query := new(dns.Msg)
				if err := query.Unpack(body); err != nil {
					t.Error(err)
					return
				}
				if query.Response || query.RecursionDesired || query.Opcode != dns.OpcodeQuery || len(query.Question) != 1 || query.Question[0].Qclass != dns.ClassINET || query.IsEdns0() != nil {
					t.Error("unexpected recursive, discovery or non-query exchange")
					return
				}
				response := handler(query)
				frame := make([]byte, 2+len(response))
				binary.BigEndian.PutUint16(frame, uint16(len(response)))
				copy(frame[2:], response)
				_, _ = conn.Write(frame)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		// Prevent late registration after listener shutdown, then close active connections.
		mu.Lock()
		closed = true
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		group.Wait()
	})
	return listener.Addr().String()
}

func dnsHeader(name string, kind uint16) dns.RR_Header {
	return dns.RR_Header{Name: dnsQName(name), Rrtype: kind, Class: dns.ClassINET, Ttl: 60}
}

func dnsNSSet(zone string, names []string) []dns.RR {
	records := make([]dns.RR, 0, len(names))
	for _, name := range names {
		records = append(records, &dns.NS{Hdr: dnsHeader(zone, dns.TypeNS), Ns: dnsQName(name)})
	}
	return records
}

func dnsSOA(zone string) dns.RR {
	return &dns.SOA{Hdr: dnsHeader(zone, dns.TypeSOA), Ns: "one.ns.cloudflare.com.", Mbox: "hostmaster.example.net.", Serial: 42, Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 60}
}

func servedFixtureRows() []map[string]any {
	rows := dnsRows(4)
	for _, row := range rows {
		row["proxied"], row["ttl"], row["name"] = false, 60, "origin.gregale.dev"
	}
	rows[1]["type"], rows[1]["content"] = "AAAA", "2001:db8::10"
	rows[2]["content"] = "192.0.2.11"
	rows[3]["name"], rows[3]["type"], rows[3]["content"] = "alias.gregale.dev", "CNAME", "outside.example.net"
	return rows
}

func newServedDNSFixture(t *testing.T, mutate func(bool, *dns.Msg)) *servedDNSFixture {
	t.Helper()
	f := &servedDNSFixture{mutate: mutate, started: make(chan struct{}), release: make(chan struct{})}
	f.probe = dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
		f.apiCalls.Add(1)
		if f.gate.CompareAndSwap(true, false) {
			close(f.started)
			<-f.release
		}
		if r.URL.Path == "/zones/"+dnsTestZone().ID {
			zone := dnsZoneBody()
			if f.changeAPI.Load() {
				zone["unknown"] = "changed"
			}
			dnsServe(t, w, dnsEnvelope(zone))
			return
		}
		dnsServe(t, w, dnsPage(servedFixtureRows(), dnsRequestPage(t, r)))
	})
	inventory, err := f.probe.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.review = ServedDNSReview{ConfigSHA256: inventory.ConfigSHA256, ParentZone: "dev", Questions: []DNSQuestion{{Name: "origin.gregale.dev", Type: "AAAA"}, {Name: "origin.gregale.dev", Type: "A"}, {Name: "alias.gregale.dev", Type: "CNAME"}}}
	for _, name := range inventory.NameServers {
		address := tcpDNSFixture(t, func(q *dns.Msg) []byte { return f.reply(t, q, false) })
		f.review.Authorities = append(f.review.Authorities, DNSAuthority{Name: name, Addresses: []string{address}})
	}
	f.review.Parents = []DNSAuthority{{Name: "ns.registry.example", Addresses: []string{tcpDNSFixture(t, func(q *dns.Msg) []byte { return f.reply(t, q, true) })}}}
	return f
}

func (f *servedDNSFixture) reply(t *testing.T, q *dns.Msg, parent bool) []byte {
	t.Helper()
	f.dnsCalls.Add(1)
	reply := new(dns.Msg)
	reply.SetReply(q)
	reply.Authoritative = true
	name := strings.TrimSuffix(strings.ToLower(q.Question[0].Name), ".")
	if parent {
		switch {
		case name == "dev" && q.Question[0].Qtype == dns.TypeSOA:
			reply.Answer = []dns.RR{dnsSOA("dev")}
		case name == "dev" && q.Question[0].Qtype == dns.TypeNS:
			reply.Answer = dnsNSSet("dev", []string{"ns.registry.example"})
		case name == "gregale.dev" && q.Question[0].Qtype == dns.TypeNS:
			reply.Authoritative = false
			reply.Ns = dnsNSSet("gregale.dev", []string{"one.ns.cloudflare.com", "two.ns.cloudflare.com"})
		default:
			t.Error("unexpected parent question", q.Question)
		}
	} else {
		switch q.Question[0].Qtype {
		case dns.TypeSOA:
			reply.Answer = []dns.RR{dnsSOA("gregale.dev")}
		case dns.TypeNS:
			reply.Answer = dnsNSSet("gregale.dev", []string{"one.ns.cloudflare.com", "two.ns.cloudflare.com"})
		case dns.TypeA:
			reply.Answer = []dns.RR{&dns.A{Hdr: dnsHeader(name, dns.TypeA), A: net.ParseIP("192.0.2.11")}, &dns.A{Hdr: dnsHeader(name, dns.TypeA), A: net.ParseIP("192.0.2.10")}}
		case dns.TypeAAAA:
			reply.Answer = []dns.RR{&dns.AAAA{Hdr: dnsHeader(name, dns.TypeAAAA), AAAA: net.ParseIP("2001:db8::10")}}
		case dns.TypeCNAME:
			reply.Answer = []dns.RR{&dns.CNAME{Hdr: dnsHeader(name, dns.TypeCNAME), Target: "OUTSIDE.EXAMPLE.NET."}}
		default:
			t.Error("unexpected child question", q.Question)
		}
	}
	if f.mutate != nil {
		f.mutate(parent, reply)
	}
	body, err := reply.Pack()
	if err != nil {
		t.Error(err)
	}
	return body
}

func TestServedDNSObservesParentDelegationAndEveryReviewedOriginEndpoint(t *testing.T) {
	f := newServedDNSFixture(t, nil)
	before := string(jsonConfig(t, f.review))
	for i := 0; i < 2; i++ {
		got, err := f.probe.ObserveServed(t.Context(), f.review)
		if err != nil || got.Provider.ConfigSHA256 != f.review.ConfigSHA256 || got.CheckedAt.IsZero() || len(got.Rounds) != 2 || len(got.Rounds[0]) != 13 || f.dnsCalls.Load() != int32((i+1)*26) || f.apiCalls.Load() != int32(4+(i+1)*8) {
			t.Fatal("partial or cached served observation", got, err, f.dnsCalls.Load(), f.apiCalls.Load())
		}
		var referrals, origins int
		for _, w := range got.Rounds[0] {
			if !canonicalDigest(w.ResponseSHA256) || w.MinimumTTL != 60 {
				t.Fatal(w)
			}
			if !w.Authoritative {
				referrals++
				if w.Name != "gregale.dev" || !slices.Equal(w.Values, []string{"one.ns.cloudflare.com", "two.ns.cloudflare.com"}) {
					t.Fatal(w)
				}
			}
			if w.Name == "origin.gregale.dev" && w.Type == "A" {
				origins++
				if !slices.Equal(w.Values, []string{"192.0.2.10", "192.0.2.11"}) {
					t.Fatal(w)
				}
			}
		}
		if referrals != 1 || origins != 2 || string(jsonConfig(t, f.review)) != before {
			t.Fatal("missing endpoint or mutated caller review")
		}
	}
}

func TestServedDNSRejectsIncompleteChangedAndUnreachableReviewedScope(t *testing.T) {
	for _, mode := range []string{"missing-child", "stale-config", "unreachable-child", "provider-change"} {
		t.Run(mode, func(t *testing.T) {
			f := newServedDNSFixture(t, nil)
			switch mode {
			case "missing-child":
				f.review.Authorities = f.review.Authorities[:1]
			case "stale-config":
				f.review.ConfigSHA256 = strings.Repeat("b", 64)
			case "unreachable-child":
				f.review.Authorities[0].Addresses[0] = "127.0.0.1:1"
			case "provider-change":
				f.mutate = func(bool, *dns.Msg) { f.changeAPI.Store(true) }
			}
			got, err := f.probe.ObserveServed(t.Context(), f.review)
			if err == nil || len(got.Rounds) != 0 || got.Provider.ConfigSHA256 != "" || !got.CheckedAt.IsZero() {
				t.Fatal("incomplete or changed scope returned success", got, err)
			}
			if (mode == "missing-child" || mode == "stale-config") && f.dnsCalls.Load() != 0 {
				t.Fatal("mismatched scope began served probes")
			}
		})
	}
}

func TestServedDNSRejectsWrongDelegationAndNonmatchingOriginAnswers(t *testing.T) {
	for _, mode := range []string{"wrong-parent-ns", "wrong-referral", "authoritative-referral", "wrong-child-ns", "cached", "missing-rr", "extra-rr", "wrong-owner", "wrong-class", "alias-in-address", "negative", "foreign-glue", "unreviewed-glue", "soa-change"} {
		t.Run(mode, func(t *testing.T) {
			var soaCalls atomic.Int32
			f := newServedDNSFixture(t, func(parent bool, r *dns.Msg) {
				q := r.Question[0]
				switch mode {
				case "wrong-parent-ns":
					if parent && q.Name == "dev." && q.Qtype == dns.TypeNS {
						r.Answer = dnsNSSet("dev", []string{"other.example"})
					}
				case "wrong-referral":
					if parent && q.Name == "gregale.dev." {
						r.Ns = r.Ns[:1]
					}
				case "authoritative-referral":
					if parent && q.Name == "gregale.dev." {
						r.Authoritative = true
					}
				case "wrong-child-ns":
					if !parent && q.Qtype == dns.TypeNS {
						r.Answer = r.Answer[:1]
					}
				case "soa-change":
					if !parent && q.Qtype == dns.TypeSOA && soaCalls.Add(1) > 2 {
						r.Answer[0].(*dns.SOA).Serial++
					}
				default:
					if parent || q.Qtype != dns.TypeA {
						return
					}
					switch mode {
					case "cached":
						r.Authoritative = false
					case "missing-rr":
						r.Answer = r.Answer[:1]
					case "extra-rr":
						r.Answer = append(r.Answer, &dns.A{Hdr: dnsHeader("origin.gregale.dev", dns.TypeA), A: net.ParseIP("192.0.2.12")})
					case "wrong-owner":
						r.Answer[0].Header().Name = "elsewhere.gregale.dev."
					case "wrong-class":
						r.Answer[0].Header().Class = dns.ClassCHAOS
					case "alias-in-address":
						r.Answer = []dns.RR{&dns.CNAME{Hdr: dnsHeader("origin.gregale.dev", dns.TypeCNAME), Target: "outside.example.net."}}
					case "negative":
						r.Rcode = dns.RcodeNameError
						r.Answer = nil
					case "foreign-glue":
						r.Extra = []dns.RR{&dns.A{Hdr: dnsHeader("other.example", dns.TypeA), A: net.ParseIP("127.0.0.1")}}
					case "unreviewed-glue":
						r.Extra = []dns.RR{&dns.A{Hdr: dnsHeader("one.ns.cloudflare.com", dns.TypeA), A: net.ParseIP("127.0.0.2")}}
					}
				}
			})
			if got, err := f.probe.ObserveServed(t.Context(), f.review); err == nil || len(got.Rounds) != 0 {
				t.Fatal("invalid DNS scope accepted", mode, got, err)
			}
		})
	}
}

func TestServedDNSFreezesNestedReviewBeforeProviderNetworkWait(t *testing.T) {
	f := newServedDNSFixture(t, nil)
	f.gate.Store(true)
	done := make(chan error, 1)
	go func() {
		got, err := f.probe.ObserveServed(t.Context(), f.review)
		if err == nil && got.Review.Authorities[0].Addresses[0] == "127.0.0.1:1" {
			t.Error("observation retained caller slices")
		}
		done <- err
	}()
	<-f.started
	f.review.Authorities[0].Addresses[0] = "127.0.0.1:1"
	f.review.Questions[0].Name = "changed.gregale.dev"
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal("frozen reviewed values changed", err)
	}
}

func TestServedDNSCancellationInterruptsAWaitingTCPFrame(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	address := tcpDNSFixture(t, func(*dns.Msg) []byte { close(started); <-release; return nil })
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, _, err := exchangeServedDNS(ctx, address, DNSQuestion{Name: "gregale.dev", Type: "NS"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled TCP frame accepted")
	}
}

func TestServedDNSRejectsProxiedOriginsBeforeDNSNetwork(t *testing.T) {
	f := newServedDNSFixture(t, nil)
	inventory, err := f.probe.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"same-type", "other-type", "missing-proxy", "missing-target", "missing-record"} {
		t.Run(mode, func(t *testing.T) {
			copy := inventory
			copy.Records = slices.Clone(inventory.Records)
			switch mode {
			case "same-type":
				yes := true
				copy.Records[0].Proxied = &yes
			case "other-type":
				yes := true
				copy.Records[1].Proxied = &yes
			case "missing-proxy":
				copy.Records[0].Proxied = nil
			case "missing-target":
				copy.Records[0].Target = ""
			case "missing-record":
				copy.Records = nil
			}
			if _, err := selectedDNSValues(copy, f.review); err == nil {
				t.Fatal("ineligible configured origin became served proof", mode)
			}
		})
	}
}

func TestServedDNSAcceptsTTLVariationAndReviewedGlueWithoutGrantingLease(t *testing.T) {
	var calls atomic.Uint32
	f := newServedDNSFixture(t, func(parent bool, r *dns.Msg) {
		for _, rr := range r.Answer {
			rr.Header().Ttl = calls.Add(1)
		}
		if parent && r.Question[0].Name == "gregale.dev." {
			r.Extra = []dns.RR{&dns.A{Hdr: dnsHeader("one.ns.cloudflare.com", dns.TypeA), A: net.ParseIP("127.0.0.1")}}
		}
	})
	got, err := f.probe.ObserveServed(t.Context(), f.review)
	if err != nil || len(got.Rounds) != 2 || len(got.Rounds[0][2].Glue) != 1 || got.Rounds[0][0].MinimumTTL == got.Rounds[1][0].MinimumTTL {
		t.Fatal("TTL variation or reviewed glue rejected", got, err)
	}
}
