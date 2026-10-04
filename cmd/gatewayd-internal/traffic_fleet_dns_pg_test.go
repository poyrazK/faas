//go:build !no_pg

// adr: 531
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The packet wrapper supplies the same post-MASQUERADE identity as the HTTP
// and TCP fixtures, then sends replies to the actual local test peer. DNS
// parsing, caller lookup, binding reads and responses remain production code.
type fleetManagedSourcePacketConn struct {
	net.PacketConn
	mu    sync.Mutex
	peers map[string]net.Addr
}

func (c *fleetManagedSourcePacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	n, peer, err := c.PacketConn.ReadFrom(buffer)
	if err != nil {
		return n, peer, err
	}
	actual := peer.(*net.UDPAddr)
	source := &net.UDPAddr{IP: net.IPv4(10, 100, 0, 5), Port: actual.Port}
	c.mu.Lock()
	c.peers[source.String()] = peer
	c.mu.Unlock()
	return n, source, nil
}

func (c *fleetManagedSourcePacketConn) WriteTo(buffer []byte, source net.Addr) (int, error) {
	c.mu.Lock()
	peer := c.peers[source.String()]
	c.mu.Unlock()
	if peer == nil {
		return 0, fmt.Errorf("fixture has no local peer for %s", source)
	}
	return c.PacketConn.WriteTo(buffer, peer)
}

// The upstream fixture answers documentation addresses only. No query reaches
// system resolvers, and the query count distinguishes private answers from
// ordinary upstream forwarding or store-failure refusal.
func fleetDNSUpstream(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	queries := new(atomic.Int32)
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		queries.Add(1)
		response := new(dns.Msg)
		response.SetReply(request)
		question := request.Question[0]
		header := dns.RR_Header{Name: question.Name, Rrtype: question.Qtype, Class: dns.ClassINET, Ttl: 30}
		switch question.Qtype {
		case dns.TypeA:
			response.Answer = []dns.RR{&dns.A{Hdr: header, A: net.ParseIP("192.0.2.25").To4()}}
		case dns.TypeAAAA:
			response.Answer = []dns.RR{&dns.AAAA{Hdr: header, AAAA: net.ParseIP("2001:db8::25")}}
		}
		_ = w.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return packet.LocalAddr().String(), queries
}

func fleetDNSAlias(t *testing.T, p *fleetDaemonProcess, upstreamQueries *atomic.Int32, alias string, qtype uint16, want int, bound bool) {
	t.Helper()
	for _, network := range []string{"udp", "tcp"} {
		endpoint := p.ready.DNSUDP
		if network == "tcp" {
			endpoint = p.ready.DNSTCP
		}
		request := new(dns.Msg)
		request.SetQuestion(alias+".internal.", qtype)
		before := upstreamQueries.Load()
		client := dns.Client{Net: network, Timeout: 2 * time.Second}
		response, _, err := client.ExchangeContext(t.Context(), request, endpoint)
		if err != nil {
			t.Fatalf("%s %s: %v", network, alias, err)
		}
		if response.Rcode != want || response.Authoritative != bound {
			t.Fatalf("%s %s: rcode=%s authoritative=%v want=%s/%v response=%s", network, alias,
				dns.RcodeToString[response.Rcode], response.Authoritative, dns.RcodeToString[want], bound, response)
		}
		forwarded := int32(0)
		answers := 0
		if want == dns.RcodeSuccess {
			if !bound || qtype == dns.TypeA {
				answers = 1
			}
			if !bound {
				forwarded = 1
			}
		}
		if after := upstreamQueries.Load(); after != before+forwarded {
			t.Fatalf("%s %s: upstream queries=%d -> %d want %d new queries", network, alias, before, after, forwarded)
		}
		if len(response.Answer) != answers {
			t.Fatalf("%s %s: answers=%v want=%d", network, alias, response.Answer, answers)
		}
		for _, answer := range response.Answer {
			address, wanted := "", "10.100.0.1"
			switch record := answer.(type) {
			case *dns.A:
				address = record.A.String()
			case *dns.AAAA:
				address = record.AAAA.String()
			}
			if !bound {
				wanted = "192.0.2.25"
				if qtype == dns.TypeAAAA {
					wanted = "2001:db8::25"
				}
			}
			if address != wanted {
				t.Fatalf("%s %s: answer=%v want=%s", network, alias, answer, wanted)
			}
		}
	}
}

func fleetDNSHTTPAlias(t *testing.T, f fleetDaemonFixture, p *fleetDaemonProcess, alias string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ready.ServiceEndpoint+"/dns-identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = alias + ".internal"
	before := len(f.vm.calls("/dns-identity"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want {
		t.Fatalf("alias=%s HTTP=%d want=%d body=%q err=%v", alias, response.StatusCode, want, body, err)
	}
	attempts := 0
	if want == http.StatusOK {
		attempts = 1
	}
	if after := len(f.vm.calls("/dns-identity")); after != before+attempts {
		t.Fatalf("alias=%s status=%d RPCs=%d -> %d want %d new attempts", alias, want, before, after, attempts)
	}
}

func fleetDNSCallerBinding(t *testing.T, f fleetDaemonFixture, appID, service string) {
	t.Helper()
	caller, err := f.store.AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	caller.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
	caller.Manifest.ServiceBindings = nil
	if service != "" {
		caller.Manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_DEPENDENCY_URL", Service: service}}
	}
	if _, err := f.store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
}

func fleetDNSCallerInstances(t *testing.T, f fleetDaemonFixture, appIndex int) []state.Instance {
	t.Helper()
	var instances []state.Instance
	for index, node := range f.nodes {
		instance, err := f.store.CreateInstance(t.Context(), f.apps[appIndex].ID, f.apps[appIndex].Deployment,
			string(state.StateRunning), 128, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetInstanceRuntime(t.Context(), instance.ID, "fixture-"+instance.ID, "10.100.0.5", 22000+index); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	return instances
}

func fleetDNSCallerState(t *testing.T, f fleetDaemonFixture, instances []state.Instance, next state.State) {
	t.Helper()
	for _, instance := range instances {
		if err := f.store.UpdateInstanceState(t.Context(), instance.ID, string(next)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrafficFleetDaemonDNSUsesFreshCallerIdentity(t *testing.T) {
	f := newFleetDaemonFixture(t)
	upstream, queries := fleetDNSUpstream(t)
	fleetDNSCallerBinding(t, f, f.apps[0].ID, "fleet-retry")
	originals := fleetDNSCallerInstances(t, f, 0)
	first := startFleetDaemonDNS(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[0].Name, f.usage, true, upstream)
	second := startFleetDaemonDNS(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[1].Name, f.usage, true, upstream)
	processes := []*fleetDaemonProcess{first, second}
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeA, dns.RcodeSuccess, true)
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeAAAA, dns.RcodeSuccess, true)
		fleetDNSHTTPAlias(t, f, p, "fleet-retry", http.StatusOK)
	}
	// UUID ownership in the ordinary instance inventory differs from the
	// configured daemon node name. A DNS-only ListAllInstances cache cannot
	// establish this identity. The indexed reader joins the node name instead.
	inventory, err := f.store.ListAllInstances(t.Context())
	if err != nil || len(inventory) != 2 || inventory[0].NodeID == first.ready.NodeName || inventory[1].NodeID == second.ready.NodeName {
		t.Fatalf("caller fixture must distinguish node UUIDs and configured names: %+v %v", inventory, err)
	}
	fleetDNSCallerBinding(t, f, f.apps[1].ID, "fleet-cache")
	replacements := fleetDNSCallerInstances(t, f, 1)
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeA, dns.RcodeSuccess, false)
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeAAAA, dns.RcodeSuccess, false)
		fleetDNSHTTPAlias(t, f, p, "fleet-retry", http.StatusForbidden)
	}
	// Reuse the exact address without a DNS/cache TTL sleep or daemon restart.
	reused := time.Now()
	fleetDNSCallerState(t, f, originals[:1], state.StateStopped)
	fleetDNSAlias(t, first, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, true)
	fleetDNSHTTPAlias(t, f, first, "fleet-cache", http.StatusOK)
	// The same address on the other node is still ambiguous. A lookup that
	// drops the configured node-name filter must not qualify this transition.
	fleetDNSAlias(t, second, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, false)
	fleetDNSHTTPAlias(t, f, second, "fleet-cache", http.StatusForbidden)
	fleetDNSCallerState(t, f, originals[1:], state.StateStopped)
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, true)
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeA, dns.RcodeSuccess, false)
		fleetDNSAlias(t, p, queries, "fleet-retry", dns.TypeAAAA, dns.RcodeSuccess, false)
		fleetDNSHTTPAlias(t, f, p, "fleet-cache", http.StatusOK)
		fleetDNSHTTPAlias(t, f, p, "fleet-retry", http.StatusForbidden)
	}
	t.Logf("DNS/HTTP address reuse visible in both live daemons in %s", time.Since(reused))
	for _, next := range []state.State{state.StateWaking, state.StateDraining, state.StateStopped, state.StateRunning} {
		fleetDNSCallerState(t, f, replacements, next)
		bound := next == state.StateRunning || next == state.StateDraining
		httpCode := http.StatusForbidden
		if bound {
			httpCode = http.StatusOK
		}
		for _, p := range processes {
			fleetDNSAlias(t, p, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, bound)
			fleetDNSHTTPAlias(t, f, p, "fleet-cache", httpCode)
		}
	}
	fleetDNSCallerBinding(t, f, f.apps[1].ID, "")
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, false)
		fleetDNSHTTPAlias(t, f, p, "fleet-cache", http.StatusForbidden)
	}
	fleetDNSCallerBinding(t, f, f.apps[1].ID, "fleet-cache")
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE instances RENAME TO instances_identity_offline"); err != nil {
		t.Fatal(err)
	}
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-cache", dns.TypeA, dns.RcodeServerFailure, false)
		fleetDNSHTTPAlias(t, f, p, "fleet-cache", http.StatusServiceUnavailable)
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE instances_identity_offline RENAME TO instances"); err != nil {
		t.Fatal(err)
	}
	for _, p := range processes {
		fleetDNSAlias(t, p, queries, "fleet-cache", dns.TypeA, dns.RcodeSuccess, true)
		fleetDNSHTTPAlias(t, f, p, "fleet-cache", http.StatusOK)
	}
	t.Log("production UDP/TCP DNS and HTTP agree on source reuse, ambiguity, live states, binding removal and lookup outage/recovery")
}
