// adr: 20261104160000
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type pendingDNSGrantRunner struct {
	fakeInputRunner
	entered, release chan struct{}
}

func (r *pendingDNSGrantRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if strings.Contains(string(input), "198.51.100.66 . 5432") {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.fakeInputRunner.RunInput(ctx, argv, input)
}

type dnsCleanupVMM struct {
	*fakeVMM
	killed chan struct{}
	once   sync.Once
}

func (v *dnsCleanupVMM) Kill(ctx context.Context, lease Lease) error {
	err := v.fakeVMM.Kill(ctx, lease)
	v.once.Do(func() { close(v.killed) })
	return err
}

func TestResolvedEgressFinishesBeforeNamespaceRelease(t *testing.T) {
	run := &pendingDNSGrantRunner{entered: make(chan struct{}), release: make(chan struct{})}
	vmm := &dnsCleanupVMM{fakeVMM: &fakeVMM{}, killed: make(chan struct{})}
	m := newTestManager(run, vmm)
	source := wakeGated(t, m, "dns-drain", "app")
	owner := resolvedOwner(t, m, source)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(run.release) }) }
	t.Cleanup(release)
	grantDone, cleanupDone := make(chan error, 1), make(chan error, 1)
	go func() {
		grantDone <- m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{netip.MustParseAddr("198.51.100.66")}, time.Minute, 5432)
	}()
	select {
	case <-run.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("grant did not reach nft")
	}
	go func() { cleanupDone <- m.Destroy(t.Context(), "dns-drain") }()
	select {
	case <-vmm.killed:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("cleanup did not kill original guest")
	}
	select {
	case err := <-cleanupDone:
		release()
		t.Fatalf("namespace released before pending grant finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	for _, done := range []<-chan error{grantDone, cleanupDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("grant/cleanup did not drain")
		}
	}
	if m.LeasedCount() != 0 {
		t.Fatal("completed grant kept the old network lease")
	}
}

func TestResolvedEgressRejectsRetiredOwnerAfterAddressReuse(t *testing.T) {
	for _, sameID := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-instance", true: "same-instance-id"}[sameID], func(t *testing.T) {
			run := &fakeInputRunner{}
			m := newTestManager(run, &fakeVMM{})
			source := wakeGated(t, m, "old-instance", "old-app")
			owner := resolvedOwner(t, m, source)
			if err := m.Destroy(t.Context(), "old-instance"); err != nil {
				t.Fatal(err)
			}
			id := "new-instance"
			if sameID {
				id = "old-instance"
			}
			if fresh := wakeGated(t, m, id, "new-app"); fresh != source {
				t.Fatalf("fixture did not reuse source: %s != %s", fresh, source)
			}
			before := run.inputRuns
			address := netip.MustParseAddr("198.51.100.66")
			if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{address}, time.Minute, 5432); !errors.Is(err, ErrResolvedEgressNoInstance) {
				t.Fatalf("retired owner result: %v", err)
			}
			if run.inputRuns != before || len(m.live[id].databaseEgress) != 0 || len(m.appResolved["new-app"]) != 0 || len(m.appDatabaseResolved["old-app"]) != 0 {
				t.Fatal("retired DNS grant changed replacement policy or app caches")
			}
			current := resolvedOwner(t, m, source)
			if owner.NetworkGeneration == "" || current.NetworkGeneration == owner.NetworkGeneration {
				t.Fatal("network generation survived instance replacement")
			}
			if err := m.AllowResolvedEgress(t.Context(), source, current, []netip.Addr{address}, time.Minute, 5432); err != nil || run.inputRuns != before+1 {
				t.Fatalf("current owner did not receive grant: %v", err)
			}
		})
	}
}

func TestResolvedEgressSeedsOriginalExpiry(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	wakeGated(t, m, "seed-target", "cached-app")
	now := time.Now()
	address := netip.MustParseAddr("198.51.100.77")
	v6 := netip.MustParseAddr("2001:db8::77")
	expired := netip.MustParseAddr("198.51.100.78")
	short := netip.MustParseAddr("198.51.100.79")
	dnsExpiry, dbExpiry := now.Add(5*time.Second), now.Add(9*time.Second)
	m.appResolved = map[string]map[netip.Addr]time.Time{"cached-app": {
		address: dnsExpiry, v6: dbExpiry, expired: now.Add(-time.Second), short: now.Add(time.Millisecond),
	}}
	m.appDatabaseResolved = map[string]map[databaseEgressKey]time.Time{"cached-app": {
		{address, 5432}: dbExpiry, {v6, 6543}: dnsExpiry, {expired, 5432}: now.Add(-time.Second), {short, 5432}: now.Add(time.Millisecond),
	}}
	m.seedResolvedEgress(t.Context(), "seed-target")
	inst := m.live["seed-target"]
	if !inst.resolvedEgress[address].Equal(dnsExpiry) || !inst.databaseEgress[databaseEgressKey{address, 5432}].Equal(dbExpiry) || !inst.resolvedEgress[v6].Equal(dbExpiry) {
		t.Fatal("seeding renewed the cached expiry")
	}
	if len(inst.resolvedEgress) != 2 || len(inst.databaseEgress) != 2 {
		t.Fatal("expired or subsecond grants were seeded")
	}
	policy := string(run.input)
	for _, want := range []string{"198.51.100.77 timeout 4s", "198.51.100.77 . 5432 timeout 8s", "2001:db8::77 timeout 8s", "2001:db8::77 . 6543 timeout 4s"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("missing remaining lifetime %q: %s", want, policy)
		}
	}
	if strings.Contains(policy, "timeout 600s") || strings.Contains(policy, expired.String()) || strings.Contains(policy, short.String()) {
		t.Fatalf("seeding extended expired permission: %s", policy)
	}
}

func TestResolvedEgressRefusesPendingCleanup(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	source := wakeGated(t, m, "retiring", "app")
	owner := resolvedOwner(t, m, source)
	inst := m.live["retiring"]
	m.retainCleanup(inst.Lease, inst.Net, nil)
	before := run.inputRuns
	if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, time.Minute, 5432); !errors.Is(err, ErrResolvedEgressNoInstance) || run.inputRuns != before {
		t.Fatalf("retiring namespace received a DNS grant: %v", err)
	}
	m.appResolved = map[string]map[netip.Addr]time.Time{"app": {netip.MustParseAddr("198.51.100.20"): time.Now().Add(time.Minute)}}
	m.seedResolvedEgress(t.Context(), "retiring")
	if run.inputRuns != before {
		t.Fatal("retiring namespace received a seeded grant")
	}
}

func TestServiceCallerGenerationSurvivesQueriesAndFencesNonAppSlots(t *testing.T) {
	for _, kind := range []string{"app", "builder", "job"} {
		t.Run(kind, func(t *testing.T) {
			m := newTestManager(&fakeInputRunner{}, &fakeVMM{})
			source := wakeGated(t, m, "caller", "app")
			inst := m.live["caller"]
			inst.Lease.IsBuilder = kind == "builder"
			inst.IsJob = kind == "job"
			first, second := resolvedOwner(t, m, source), resolvedOwner(t, m, source)
			if first.InstanceID != "caller" || first.NetworkGeneration == "" || first.NetworkGeneration != second.NetworkGeneration {
				t.Fatal("caller lost its immutable generation")
			}
			if kind != "app" && first.AppID != "" {
				t.Fatal("non-app DNS caller received database app identity")
			}
			if err := m.AllowResolvedEgress(t.Context(), source, first, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, time.Minute, 0); err != nil {
				t.Fatalf("occupied slot cannot resolve public addresses: %v", err)
			}
		})
	}
}
