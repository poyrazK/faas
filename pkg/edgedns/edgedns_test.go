// adr: 20261102081618
// edgedns_test.go pins how the managed names' records follow the compute
// nodes: probes in a row decide, a drain is immediate, and the last record
// never goes. A name's records all take its mode, proxied or DNS-only
// (ADR-20261103163954 §1).
package edgedns_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgedns"
)

var (
	wildcard = edgedns.Name{ZoneID: "zone-1", Host: "*.gregale.dev"}
	addrA    = netip.MustParseAddr("203.0.113.1")
	addrB    = netip.MustParseAddr("203.0.113.2")
)

func TestPlan(t *testing.T) {
	rec := func(id string, a netip.Addr) edgedns.Record { return edgedns.Record{ID: id, Addr: a} }
	for _, tc := range []struct {
		name    string
		current []edgedns.Record
		desired []netip.Addr
		want    []string
	}{
		{name: "first node", desired: []netip.Addr{addrA}, want: []string{"create *.gregale.dev -> 203.0.113.1"}},
		{name: "a node joins", current: []edgedns.Record{rec("r1", addrA)}, desired: []netip.Addr{addrA, addrB}, want: []string{"create *.gregale.dev -> 203.0.113.2"}},
		{name: "a node leaves", current: []edgedns.Record{rec("r1", addrA), rec("r2", addrB)}, desired: []netip.Addr{addrA}, want: []string{"delete *.gregale.dev -> 203.0.113.2 (r2)"}},
		{name: "replace creates first", current: []edgedns.Record{rec("r1", addrA)}, desired: []netip.Addr{addrB}, want: []string{"create *.gregale.dev -> 203.0.113.2", "delete *.gregale.dev -> 203.0.113.1 (r1)"}},
		{name: "no node keeps every record", current: []edgedns.Record{rec("r1", addrA)}},
		{name: "in step", current: []edgedns.Record{rec("r1", addrA)}, desired: []netip.Addr{addrA}},
		{name: "DNS-only records go back behind Cloudflare", current: []edgedns.Record{dnsOnly("r1", addrA)}, desired: []netip.Addr{addrA}, want: []string{"make *.gregale.dev -> 203.0.113.1 (r1) proxied"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range edgedns.Plan(wildcard, tc.current, tc.desired) {
				got = append(got, c.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Plan = %q, want %q", got, tc.want)
			}
		})
	}
}

func dnsOnly(id string, a netip.Addr) edgedns.Record {
	return edgedns.Record{ID: id, Addr: a, DNSOnly: true}
}

func TestPlanDNSOnly(t *testing.T) {
	pilot := edgedns.Name{ZoneID: "zone-1", Host: "pilot.gregale.dev", DNSOnly: true}
	proxied := func(id string, a netip.Addr) edgedns.Record { return edgedns.Record{ID: id, Addr: a} }
	for _, tc := range []struct {
		name    string
		current []edgedns.Record
		desired []netip.Addr
		want    []string
	}{
		{name: "created DNS-only", desired: []netip.Addr{addrA}, want: []string{"create pilot.gregale.dev -> 203.0.113.1"}},
		{
			name:    "the name moves whole: a create, then the kept record's switch, then the delete",
			current: []edgedns.Record{proxied("r1", addrA), proxied("r2", addrB)},
			desired: []netip.Addr{addrA, netip.MustParseAddr("203.0.113.3")},
			want: []string{
				"create pilot.gregale.dev -> 203.0.113.3",
				"make pilot.gregale.dev -> 203.0.113.1 (r1) dns-only",
				"delete pilot.gregale.dev -> 203.0.113.2 (r2)",
			},
		},
		{
			name:    "with no node ready every record still switches and none goes",
			current: []edgedns.Record{proxied("r1", addrA), dnsOnly("r2", addrB)},
			want:    []string{"make pilot.gregale.dev -> 203.0.113.1 (r1) dns-only"},
		},
		{name: "in step", current: []edgedns.Record{dnsOnly("r1", addrA)}, desired: []netip.Addr{addrA}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range edgedns.Plan(pilot, tc.current, tc.desired) {
				got = append(got, c.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Plan = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlanCapsADNSOnlyNameAndKeepsTheListedAddresses(t *testing.T) {
	pilot := edgedns.Name{ZoneID: "zone-1", Host: "pilot.gregale.dev", DNSOnly: true}
	limit := api.EdgeDNSOnlyAddrsPerNameMax
	var desired []netip.Addr
	for i := range limit + 4 {
		desired = append(desired, netip.MustParseAddr(fmt.Sprintf("203.0.113.%d", 10+i)))
	}
	// The last address is already listed: it stays, and only limit-1 of the
	// others join it.
	listed := desired[len(desired)-1]
	current := []edgedns.Record{dnsOnly("r-listed", listed)}
	var creates []netip.Addr
	for _, c := range edgedns.Plan(pilot, current, desired) {
		if c.Op != edgedns.OpCreate {
			t.Fatalf("Plan touched the listed record: %s", c)
		}
		creates = append(creates, c.Addr)
	}
	if want := desired[:limit-1]; !slices.Equal(creates, want) {
		t.Fatalf("creates = %v, want the first %d desired", creates, len(want))
	}
	// A proxied name has no cap: Cloudflare answers with its own addresses.
	if got := edgedns.Plan(wildcard, nil, desired); len(got) != len(desired) {
		t.Fatalf("a proxied name planned %d creates, want %d", len(got), len(desired))
	}
}

func TestReconcilerMovesANameToDNSOnlyWithoutDroppingARecord(t *testing.T) {
	dns := newFakeDNS(addrA, addrB)
	pilot := edgedns.Name{ZoneID: wildcard.ZoneID, Host: wildcard.Host, DNSOnly: true}
	r, err := edgedns.NewReconciler(edgedns.Config{Names: []edgedns.Name{pilot}}, dns, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []edgedns.Node{
		{Name: "a", Public: addrA, Active: true, Ready: true},
		{Name: "b", Public: addrB, Active: true, Ready: true},
	}
	if err := r.Step(context.Background(), nodes); err != nil {
		t.Fatal(err)
	}
	for _, rec := range dns.records[wildcard.Host] {
		if !rec.DNSOnly {
			t.Fatalf("record %s is still proxied after the tick", rec.ID)
		}
	}
	if got := dns.addrs(); !slices.Equal(got, []netip.Addr{addrA, addrB}) {
		t.Fatalf("records = %v, want both kept", got)
	}
	if dns.writes != 2 {
		t.Fatalf("writes = %d, want one switch per record", dns.writes)
	}
}

// fakeDNS is one zone's address records in memory.
type fakeDNS struct {
	records    map[string][]edgedns.Record
	cname      bool
	failCreate bool
	writes     int
	nextID     int
}

func newFakeDNS(addrs ...netip.Addr) *fakeDNS {
	f := &fakeDNS{records: map[string][]edgedns.Record{}}
	for _, a := range addrs {
		f.nextID++
		f.records[wildcard.Host] = append(f.records[wildcard.Host], edgedns.Record{ID: "r" + a.String(), Addr: a})
	}
	return f
}

func (f *fakeDNS) Records(_ context.Context, name edgedns.Name) ([]edgedns.Record, bool, error) {
	return slices.Clone(f.records[name.Host]), f.cname, nil
}

func (f *fakeDNS) Create(_ context.Context, name edgedns.Name, addr netip.Addr) error {
	if f.failCreate {
		return errors.New("create refused")
	}
	f.writes++
	f.records[name.Host] = append(f.records[name.Host], edgedns.Record{ID: "r" + addr.String(), Addr: addr, DNSOnly: name.DNSOnly})
	return nil
}

func (f *fakeDNS) SetMode(_ context.Context, name edgedns.Name, id string) error {
	f.writes++
	for i, r := range f.records[name.Host] {
		if r.ID == id {
			f.records[name.Host][i].DNSOnly = name.DNSOnly
		}
	}
	return nil
}

func (f *fakeDNS) Delete(_ context.Context, name edgedns.Name, id string) error {
	f.writes++
	f.records[name.Host] = slices.DeleteFunc(f.records[name.Host], func(r edgedns.Record) bool { return r.ID == id })
	return nil
}

func (f *fakeDNS) addrs() []netip.Addr {
	var out []netip.Addr
	for _, r := range f.records[wildcard.Host] {
		out = append(out, r.Addr)
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out
}

type fixture struct {
	t       *testing.T
	dns     *fakeDNS
	rec     *edgedns.Reconciler
	metrics *edgedns.Metrics
	reg     *prometheus.Registry
}

func newFixture(t *testing.T, dns *fakeDNS, dryRun bool) *fixture {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := edgedns.NewMetrics(reg)
	r, err := edgedns.NewReconciler(edgedns.Config{Names: []edgedns.Name{wildcard}, DryRun: dryRun}, dns, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dns: dns, rec: r, metrics: m, reg: reg}
}

// tick runs one step with node A and B in the given states.
func (f *fixture) tick(aReady, bReady, bActive bool) {
	f.t.Helper()
	nodes := []edgedns.Node{
		{Name: "a", Public: addrA, Active: true, Ready: aReady},
		{Name: "b", Public: addrB, Active: bActive, Ready: bReady},
	}
	if err := f.rec.Step(context.Background(), nodes); err != nil {
		f.t.Fatalf("Step: %v", err)
	}
}

func (f *fixture) want(addrs ...netip.Addr) {
	f.t.Helper()
	if got := f.dns.addrs(); !slices.Equal(got, addrs) {
		f.t.Fatalf("records = %v, want %v", got, addrs)
	}
}

func TestReconcilerWithdrawsAfterMissesAndRestoresAfterAnswers(t *testing.T) {
	f := newFixture(t, newFakeDNS(addrA, addrB), false)
	for range edgedns.DefaultFailAfter - 1 {
		f.tick(true, false, true)
		f.want(addrA, addrB)
	}
	f.tick(true, false, true)
	f.want(addrA)
	for range edgedns.DefaultRestoreAfter - 1 {
		f.tick(true, true, true)
		f.want(addrA)
	}
	f.tick(true, true, true)
	f.want(addrA, addrB)
	if n := testutil.CollectAndCount(f.reg, "edge_dns_probe_failures_total"); n != 1 {
		t.Fatalf("edge_dns_probe_failures_total series = %d, want node b's", n)
	}
}

func TestReconcilerWithdrawsADrainedNodeAtOnce(t *testing.T) {
	f := newFixture(t, newFakeDNS(addrA, addrB), false)
	f.tick(true, true, false)
	f.want(addrA)
}

func TestReconcilerNeverRemovesTheLastRecord(t *testing.T) {
	f := newFixture(t, newFakeDNS(addrA, addrB), false)
	for range 10 {
		f.tick(false, false, true)
	}
	f.want(addrA, addrB)

	// With one node left in, the other goes, and that one stays even after
	// it fails too.
	f = newFixture(t, newFakeDNS(addrA), false)
	for range 10 {
		f.tick(false, false, true)
	}
	f.want(addrA)
}

func TestReconcilerRestartChangesNothing(t *testing.T) {
	dns := newFakeDNS(addrA)
	f := newFixture(t, dns, false)
	f.tick(true, true, true)
	if dns.writes != 0 {
		t.Fatalf("the first tick wrote %d records", dns.writes)
	}
	// B was not behind the name, so it earns its place like a new node.
	for range edgedns.DefaultRestoreAfter - 2 {
		f.tick(true, true, true)
	}
	f.want(addrA)
	f.tick(true, true, true)
	f.want(addrA, addrB)
}

func TestReconcilerLeavesANameACNAMEHolds(t *testing.T) {
	dns := newFakeDNS()
	dns.cname = true
	f := newFixture(t, dns, false)
	for range edgedns.DefaultRestoreAfter + 1 {
		f.tick(true, true, true)
	}
	if dns.writes != 0 {
		t.Fatalf("wrote %d records beside a CNAME", dns.writes)
	}
}

func TestReconcilerDryRunWritesNothing(t *testing.T) {
	dns := newFakeDNS(addrA, addrB)
	f := newFixture(t, dns, true)
	f.tick(true, true, false)
	if dns.writes != 0 {
		t.Fatalf("dry run wrote %d records", dns.writes)
	}
	f.want(addrA, addrB)
	if n := testutil.CollectAndCount(f.reg, "edge_dns_changes_total"); n != 1 {
		t.Fatalf("edge_dns_changes_total series = %d, want the one dry-run delete", n)
	}
}

func TestReconcilerKeepsTheOldRecordWhenItsReplacementFails(t *testing.T) {
	dns := newFakeDNS(addrA)
	f := newFixture(t, dns, false)
	dns.failCreate = true
	// A drains while B, never behind the name, earns its place: B's create
	// fails, so A's record must stay.
	for range edgedns.DefaultRestoreAfter {
		_ = f.rec.Step(context.Background(), []edgedns.Node{
			{Name: "a", Public: addrA, Active: false, Ready: true},
			{Name: "b", Public: addrB, Active: true, Ready: true},
		})
	}
	f.want(addrA)
}

func TestReconcilerKeepsANodeWithoutAnAddressOut(t *testing.T) {
	dns := newFakeDNS(addrA)
	f := newFixture(t, dns, false)
	for range edgedns.DefaultRestoreAfter + 1 {
		if err := f.rec.Step(context.Background(), []edgedns.Node{
			{Name: "a", Public: addrA, Active: true, Ready: true},
			{Name: "c", Active: true, Ready: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	f.want(addrA)
}

func TestNewReconcilerNeedsNames(t *testing.T) {
	if _, err := edgedns.NewReconciler(edgedns.Config{}, newFakeDNS(), nil, nil); err == nil {
		t.Fatal("a reconciler with no names was accepted")
	}
}

func TestReconcilerCountsFailedCloudflareCalls(t *testing.T) {
	dns := newFakeDNS(addrA)
	f := newFixture(t, dns, false)
	dns.failCreate = true
	for range edgedns.DefaultRestoreAfter {
		_ = f.rec.Step(context.Background(), []edgedns.Node{
			{Name: "a", Public: addrA, Active: true, Ready: true},
			{Name: "b", Public: addrB, Active: true, Ready: true},
		})
	}
	got, err := testutil.GatherAndCount(f.reg, "edge_dns_errors_total")
	if err != nil || got != 4 {
		t.Fatalf("edge_dns_errors_total series = %d %v, want read, create, mode and delete", got, err)
	}
	if v := testutil.ToFloat64(f.metrics.ErrorsForTest("create")); v != 1 {
		t.Fatalf("create errors = %v, want 1", v)
	}
}

func TestReconcilerCountsProbeFailuresOnlyForNodesThatCouldServe(t *testing.T) {
	f := newFixture(t, newFakeDNS(addrA), false)
	_ = f.rec.Step(context.Background(), []edgedns.Node{
		{Name: "a", Public: addrA, Active: true, Ready: true},
		{Name: "drained", Public: addrB, Active: false, Ready: false},
		{Name: "no-address", Active: true, Ready: false},
	})
	if n := testutil.CollectAndCount(f.reg, "edge_dns_probe_failures_total"); n != 0 {
		t.Fatalf("probe failures counted for %d nodes that could not serve", n)
	}
}
