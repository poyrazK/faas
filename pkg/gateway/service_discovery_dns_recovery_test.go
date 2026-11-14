// adr: 20261104150000
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Both protocols use the same upstream address, as a real recursive resolver.
func startServiceDNSFixture(t *testing.T, handler dns.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp4", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	udp := &dns.Server{PacketConn: packet, Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}
	tcp := &dns.Server{Listener: listener, Net: "tcp", Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}
	t.Cleanup(func() { _ = udp.Shutdown(); _ = tcp.Shutdown() })
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	for range 2 {
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("DNS fixture did not start")
		}
	}
	return listener.Addr().String()
}

func queryServiceDNSFixture(t *testing.T, network, address string, udpSize uint16) *dns.Msg {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion("allowed.example.", dns.TypeA)
	if udpSize != 0 {
		req.SetEdns0(udpSize, true)
	}
	resp, _, err := (&dns.Client{Net: network, UDPSize: 4096, Timeout: 3 * time.Second}).ExchangeContext(t.Context(), req, address)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func serviceDNSAddress(name, address string) dns.RR {
	return &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300}, A: net.ParseIP(address)}
}

func serviceDNSAlias(name, target string) dns.RR {
	return &dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300}, Target: target}
}

func TestServiceDiscoveryDNSChecksUpstreamAliasPolicy(t *testing.T) {
	for _, tc := range []struct {
		name              string
		answers           []dns.RR
		disabled, blocked bool
	}{
		{"blocked-cname", []dns.RR{serviceDNSAlias("allowed.example.", "pool.supportxmr.com."), serviceDNSAddress("pool.supportxmr.com.", "198.51.100.44")}, false, true},
		{"cname-without-address", []dns.RR{serviceDNSAlias("allowed.example.", "pool.supportxmr.com.")}, false, true},
		{"blocked-intermediate", []dns.RR{serviceDNSAlias("allowed.example.", "pool.supportxmr.com."), serviceDNSAlias("pool.supportxmr.com.", "edge.example."), serviceDNSAddress("edge.example.", "198.51.100.44")}, false, true},
		{"operator-chain", []dns.RR{serviceDNSAlias("allowed.example.", "step.example."), serviceDNSAlias("step.example.", "HOST.MALWARE.EXAMPLE."), serviceDNSAddress("HOST.MALWARE.EXAMPLE.", "198.51.100.44")}, false, true},
		{"blocked-owner", []dns.RR{serviceDNSAddress("pool.supportxmr.com.", "198.51.100.44")}, false, true},
		{"blocked-dname", []dns.RR{&dns.DNAME{Hdr: dns.RR_Header{Name: "example.", Rrtype: dns.TypeDNAME, Class: dns.ClassINET, Ttl: 300}, Target: "supportxmr.com."}, serviceDNSAlias("allowed.example.", "allowed.supportxmr.com."), serviceDNSAddress("allowed.supportxmr.com.", "198.51.100.44")}, false, true},
		{"allowed-alias", []dns.RR{serviceDNSAlias("allowed.example.", "edge.example."), serviceDNSAddress("edge.example.", "198.51.100.44")}, false, false},
		{"disabled-blocklist", []dns.RR{serviceDNSAlias("allowed.example.", "pool.supportxmr.com."), serviceDNSAddress("pool.supportxmr.com.", "198.51.100.44")}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg)
				resp.SetReply(req)
				resp.Answer = tc.answers
				_ = w.WriteMsg(resp)
			}))
			var attributed, blocked, granted, backupQueries atomic.Int32
			backup := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				backupQueries.Add(1)
				resp := new(dns.Msg)
				resp.SetReply(req)
				resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "198.51.100.44")}
				_ = w.WriteMsg(resp)
			}))
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{upstream, backup}, nil, func(ctx context.Context, remote string) (string, error) {
				if _, ok := ctx.Deadline(); !ok || remote == "" {
					t.Error("blocked answer lost attribution context")
				}
				attributed.Add(1)
				return "caller-app", nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var blocklist *DNSBlocklist
			if !tc.disabled {
				blocklist = NewDNSBlocklist(map[string]string{"malware.example": "c2"})
			}
			h.WithBlocklist(blocklist, func(string) { blocked.Add(1) })
			h.WithResolvedEgressHook(func(_ context.Context, _, name string, addrs []netip.Addr, _ time.Duration) error {
				if name != "allowed.example" || len(addrs) != 1 || addrs[0].String() != "198.51.100.44" {
					t.Errorf("incorrect grant: name=%s addresses=%v", name, addrs)
				}
				granted.Add(1)
				return nil
			})
			resp := queryServiceDNSFixture(t, "udp", startServiceDNSFixture(t, h), 0)
			if backupQueries.Load() != 0 {
				t.Fatal("complete answer or policy denial retried through a backup")
			}
			if tc.blocked {
				if resp.Rcode != dns.RcodeNameError || len(resp.Answer) != 0 || granted.Load() != 0 || blocked.Load() != 1 || attributed.Load() != 1 {
					t.Fatalf("blocked alias escaped: response=%s grants=%d blocked=%d attributed=%d", resp, granted.Load(), blocked.Load(), attributed.Load())
				}
			} else if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != len(tc.answers) || granted.Load() != 1 || blocked.Load() != 0 || attributed.Load() != 0 {
				t.Fatalf("allowed alias failed: response=%s grants=%d blocked=%d attributed=%d", resp, granted.Load(), blocked.Load(), attributed.Load())
			}
		})
	}
}

func TestServiceDiscoveryDNSRetriesTruncatedReplies(t *testing.T) {
	for _, tc := range []struct {
		name, network string
		udpSize       uint16
		truncated     bool
	}{
		{"udp-edns", "udp", 4096, false},
		{"udp-minimum", "udp", 0, true},
		{"tcp", "tcp", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var udpQueries, tcpQueries, grants atomic.Int32
			upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg)
				resp.SetReply(req)
				if _, tcp := w.RemoteAddr().(*net.TCPAddr); !tcp {
					udpQueries.Add(1)
					resp.Truncated = true
					resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "192.0.2.250")}
				} else {
					tcpQueries.Add(1)
					for i := 1; i <= 40; i++ {
						resp.Answer = append(resp.Answer, serviceDNSAddress(req.Question[0].Name, fmt.Sprintf("198.51.100.%d", i)))
					}
				}
				_ = w.WriteMsg(resp)
			}))
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{upstream}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithResolvedEgressHook(func(_ context.Context, _, _ string, addrs []netip.Addr, _ time.Duration) error {
				if len(addrs) != 40 {
					t.Errorf("partial UDP answer authorized: %v", addrs)
				}
				for _, addr := range addrs {
					if addr.String() == "192.0.2.250" {
						t.Error("truncated address was granted")
					}
				}
				grants.Add(1)
				return nil
			})
			edge := startServiceDNSFixture(t, h)
			resp := queryServiceDNSFixture(t, tc.network, edge, tc.udpSize)
			if resp.Truncated != tc.truncated || resp.Rcode != dns.RcodeSuccess || udpQueries.Load() != 1 || tcpQueries.Load() != 1 || grants.Load() != 1 {
				t.Fatalf("TCP retry failed: response=%s UDP=%d TCP=%d grants=%d", resp, udpQueries.Load(), tcpQueries.Load(), grants.Load())
			}
			if tc.truncated {
				resp.Compress = true
				if resp.Len() > dns.MinMsgSize {
					t.Fatalf("UDP response exceeds guest buffer: %d", resp.Len())
				}
				resp = queryServiceDNSFixture(t, "tcp", edge, 0)
			}
			if resp.Truncated || len(resp.Answer) != 40 {
				t.Fatalf("complete answer unavailable: %s", resp)
			}
		})
	}
}

func TestServiceDiscoveryDNSRejectsIncompleteUpstreams(t *testing.T) {
	for _, tc := range []struct {
		name              string
		tcpRcode          int
		truncated, backup bool
	}{
		{"tcp-still-truncated", dns.RcodeSuccess, true, true},
		{"tcp-servfail", dns.RcodeServerFailure, false, true},
		{"all-truncated", dns.RcodeSuccess, true, false},
		{"all-servfail", dns.RcodeServerFailure, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tcpQueries, backupQueries, grants atomic.Int32
			upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg)
				resp.SetReply(req)
				resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "192.0.2.250")}
				if _, tcp := w.RemoteAddr().(*net.TCPAddr); tcp {
					tcpQueries.Add(1)
					resp.Rcode = tc.tcpRcode
					resp.Truncated = tc.truncated
				} else {
					resp.Truncated = true
				}
				_ = w.WriteMsg(resp)
			}))
			upstreams := []string{upstream}
			if tc.backup {
				upstreams = append(upstreams, startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
					backupQueries.Add(1)
					resp := new(dns.Msg)
					resp.SetReply(req)
					resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "198.51.100.44")}
					_ = w.WriteMsg(resp)
				})))
			}
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), upstreams, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithResolvedEgressHook(func(_ context.Context, _, _ string, addrs []netip.Addr, _ time.Duration) error {
				if len(addrs) != 1 || addrs[0].String() != "198.51.100.44" {
					t.Errorf("incomplete answer granted: %v", addrs)
				}
				grants.Add(1)
				return nil
			})
			resp := queryServiceDNSFixture(t, "udp", startServiceDNSFixture(t, h), 0)
			if tcpQueries.Load() != 1 {
				t.Fatalf("missing TCP retry: %d", tcpQueries.Load())
			}
			if tc.backup {
				if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 || backupQueries.Load() != 1 || grants.Load() != 1 {
					t.Fatalf("backup failed: response=%s backup=%d grants=%d", resp, backupQueries.Load(), grants.Load())
				}
			} else if resp.Rcode != dns.RcodeServerFailure || len(resp.Answer) != 0 || grants.Load() != 0 {
				t.Fatalf("failed upstream was accepted: response=%s grants=%d", resp, grants.Load())
			}
		})
	}
}

func TestServiceDiscoveryDNSUpstreamFailover(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rcode    int
		fallback bool
	}{
		{"servfail", dns.RcodeServerFailure, true},
		{"nxdomain", dns.RcodeNameError, false},
		{"nodata", dns.RcodeSuccess, false},
		{"refused", dns.RcodeRefused, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backupQueries, grants atomic.Int32
			first := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg)
				resp.SetRcode(req, tc.rcode)
				if tc.rcode == dns.RcodeServerFailure {
					resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "192.0.2.250")}
				}
				_ = w.WriteMsg(resp)
			}))
			backup := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				backupQueries.Add(1)
				resp := new(dns.Msg)
				resp.SetReply(req)
				resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "198.51.100.44")}
				_ = w.WriteMsg(resp)
			}))
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{first, backup}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithResolvedEgressHook(func(_ context.Context, _, _ string, addrs []netip.Addr, _ time.Duration) error {
				if len(addrs) != 1 || addrs[0].String() != "198.51.100.44" {
					t.Errorf("failed upstream granted addresses: %v", addrs)
				}
				grants.Add(1)
				return nil
			})
			resp := queryServiceDNSFixture(t, "udp", startServiceDNSFixture(t, h), 0)
			if tc.fallback {
				if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 || backupQueries.Load() != 1 || grants.Load() != 1 {
					t.Fatalf("healthy backup unused: response=%s backup=%d grants=%d", resp, backupQueries.Load(), grants.Load())
				}
			} else if resp.Rcode != tc.rcode || len(resp.Answer) != 0 || backupQueries.Load() != 0 || grants.Load() != 0 {
				t.Fatalf("terminal negative retried: response=%s backup=%d grants=%d", resp, backupQueries.Load(), grants.Load())
			}
		})
	}
}

func TestServiceDiscoveryDNSTCPRetryHonorsDeadline(t *testing.T) {
	hold := make(chan struct{})
	var tcpQueries atomic.Int32
	upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		if _, tcp := w.RemoteAddr().(*net.TCPAddr); tcp {
			tcpQueries.Add(1)
			<-hold
			return
		}
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Truncated = true
		_ = w.WriteMsg(resp)
	}))
	t.Cleanup(func() { close(hold) })
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	req := new(dns.Msg)
	req.SetQuestion("allowed.example.", dns.TypeA)
	start := time.Now()
	_, err := exchangeServiceDNS(ctx, req, upstream)
	if err == nil || tcpQueries.Load() != 1 || time.Since(start) > time.Second {
		t.Fatalf("TCP retry escaped deadline: queries=%d duration=%s error=%v", tcpQueries.Load(), time.Since(start), err)
	}
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("retry returned without consuming caller deadline: %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("caller deadline did not expire")
	}
}
