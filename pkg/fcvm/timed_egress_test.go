// adr: 20261105120000
package fcvm

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// Model Linux 6.8: add-existing acknowledges without renewing its timeout.
// Apply each input transaction to a copy so failures cannot partially commit.
type timedSetRunner struct {
	fakeInputRunner
	until                      map[string]time.Time
	failNext, loseAck, failAll bool
}

func (r *timedSetRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if err := r.fakeInputRunner.RunInput(ctx, argv, input); err != nil {
		return err
	}
	if r.failNext || r.failAll {
		r.failNext = false
		return errors.New("injected transaction failure")
	}
	next := maps.Clone(r.until)
	for _, line := range strings.Split(string(input), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || (fields[4] != netns.EgressResolvedSet && fields[4] != netns.EgressDatabaseSet) {
			continue
		}
		prefix := fields[2] + "/" + fields[4] + "/"
		if fields[0] == "flush" && fields[1] == "set" {
			for key := range next {
				if strings.HasPrefix(key, prefix) {
					delete(next, key)
				}
			}
			continue
		}
		if len(fields) < 8 || fields[1] != "element" {
			continue
		}
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(strings.Join(fields[5:], " ")), "{"), "}"))
		for _, element := range strings.Split(body, ", ") {
			parts := strings.Fields(element)
			key := prefix + parts[0]
			if fields[4] == netns.EgressDatabaseSet {
				key += "/" + parts[2]
			}
			if fields[0] == "delete" {
				if !next[key].After(time.Now()) {
					return errors.New("element expired before deletion")
				}
				delete(next, key)
				continue
			}
			for i, part := range parts {
				if part != "timeout" {
					continue
				}
				seconds, err := strconv.Atoi(strings.TrimSuffix(parts[i+1], "s"))
				if err != nil {
					return err
				}
				if !next[key].After(time.Now()) {
					next[key] = time.Now().Add(time.Duration(seconds) * time.Second)
				}
			}
		}
	}
	r.until = next
	if r.loseAck {
		r.loseAck = false
		return errors.New("committed but acknowledgement lost")
	}
	return nil
}

func TestDNSRefreshRenewsLiveKernelExpiration(t *testing.T) {
	r := &timedSetRunner{until: map[string]time.Time{}}
	m := newTestManager(r, &fakeVMM{})
	source := wakeGated(t, m, "renew", "app")
	owner := resolvedOwner(t, m, source)
	addresses := []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}
	for _, ttl := range []time.Duration{time.Minute, time.Hour} {
		if err := m.AllowResolvedEgress(t.Context(), source, owner, addresses, ttl, 5432); err != nil {
			t.Fatal(err)
		}
	}
	for key, until := range r.until {
		if time.Until(until) < 3500*time.Second {
			t.Errorf("kernel %s was not renewed: %s", key, time.Until(until))
		}
	}
	if len(r.until) != 4 {
		t.Fatalf("kernel permissions: %v", r.until)
	}
}

func TestTimedEgressReconcilesFailureAndLostAcknowledgement(t *testing.T) {
	for _, mode := range []string{"failed", "lost-ack", "both-fail"} {
		t.Run(mode, func(t *testing.T) {
			r := &timedSetRunner{until: map[string]time.Time{}}
			m := newTestManager(r, &fakeVMM{})
			source := wakeGated(t, m, "reconcile", "app")
			owner := resolvedOwner(t, m, source)
			old := netip.MustParseAddr("203.0.113.10")
			fresh := netip.MustParseAddr("2001:db8::20")
			if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{old}, time.Minute, 5432); err != nil {
				t.Fatal(err)
			}
			original := maps.Clone(r.until)
			originalDeadline := m.live[owner.InstanceID].resolvedEgress[old]
			r.failNext = mode == "failed"
			r.loseAck = mode == "lost-ack"
			r.failAll = mode == "both-fail"
			err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{fresh}, time.Hour, 5432)
			inst := m.live[owner.InstanceID]
			r.failAll = false
			if mode == "both-fail" {
				if err == nil || len(inst.resolvedEgress) != 1 || len(inst.databaseEgress) != 1 || !maps.Equal(original, r.until) {
					t.Fatalf("failed mutation advanced state: %v %v", err, r.until)
				}
				return
			}
			if err != nil || len(r.until) != 4 {
				t.Fatalf("reconcile: %v, kernel=%v", err, r.until)
			}
			for key := range original {
				if r.until[key].After(originalDeadline) {
					t.Fatalf("fallback renewed unrelated grant %s", key)
				}
			}
			if _, ok := inst.resolvedEgress[fresh]; !ok {
				t.Fatal("successful reconciliation not cached")
			}
		})
	}
}

func TestExpiredDNSGrantsAreRemovedFromInstanceCaches(t *testing.T) {
	r := &fakeInputRunner{}
	m := newTestManager(r, &fakeVMM{})
	source := wakeGated(t, m, "prune", "app")
	owner := resolvedOwner(t, m, source)
	inst := m.live[owner.InstanceID]
	inst.resolvedEgress = map[netip.Addr]time.Time{}
	inst.databaseEgress = map[databaseEgressKey]time.Time{}
	for i := 0; i < 8192; i++ {
		a := netip.AddrFrom4([4]byte{198, byte(30 + i/256), byte(i), 1})
		inst.resolvedEgress[a] = time.Now().Add(-time.Hour)
		inst.databaseEgress[databaseEgressKey{a, 5432}] = time.Now().Add(-time.Hour)
	}
	if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, time.Minute, 5432); err != nil {
		t.Fatal(err)
	}
	if len(inst.resolvedEgress) != 1 || len(inst.databaseEgress) != 1 {
		t.Fatalf("cache retains expired grants: DNS=%d DB=%d", len(inst.resolvedEgress), len(inst.databaseEgress))
	}
}

func TestTimedEgressCapacityPreservesValidGrants(t *testing.T) {
	for _, database := range []bool{false, true} {
		t.Run("database="+strconv.FormatBool(database), func(t *testing.T) {
			r := &fakeInputRunner{}
			m := newTestManager(r, &fakeVMM{})
			source := wakeGated(t, m, "capacity", "app")
			owner := resolvedOwner(t, m, source)
			inst := m.live[owner.InstanceID]
			inst.resolvedEgress = map[netip.Addr]time.Time{}
			inst.databaseEgress = map[databaseEgressKey]time.Time{}
			limit, port := api.DNSGatedEgressInstanceFamilyMax, uint16(0)
			if database {
				limit, port = api.DatabaseEgressInstanceFamilyMax, 5432
			}
			var existing netip.Addr
			until := time.Now().Add(5 * time.Minute)
			for i := 0; i < limit; i++ {
				a := netip.AddrFrom4([4]byte{198, byte(i >> 16), byte(i >> 8), byte(i)})
				if database {
					inst.databaseEgress[databaseEgressKey{a, port}] = until
				} else {
					inst.resolvedEgress[a] = until
				}
				existing = a
			}
			before := r.inputRuns
			fresh := netip.MustParseAddr("203.0.113.9")
			if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{fresh}, time.Hour, port); err == nil || r.inputRuns != before {
				t.Fatalf("capacity overflow reached nft: err=%v", err)
			}
			// Renewal at capacity and duplicate input do not consume additional slots.
			if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{existing, existing}, time.Hour, port); err != nil {
				t.Fatal(err)
			}
			// The IPv6 budget is independent.
			if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{netip.MustParseAddr("2001:db8::1")}, time.Hour, port); err != nil {
				t.Fatal(err)
			}
			if database && len(inst.databaseEgress) != limit+1 || !database && len(inst.resolvedEgress) != limit+1 {
				t.Fatal("valid grants evicted or duplicate keys counted")
			}
		})
	}
}

func TestTimedEgressSeedPreservesNewerGrantAndBoundsDatabasePairs(t *testing.T) {
	r := &fakeInputRunner{}
	m := newTestManager(r, &fakeVMM{})
	source := wakeGated(t, m, "seed", "app")
	owner := resolvedOwner(t, m, source)
	address := netip.MustParseAddr("203.0.113.9")
	if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{address}, time.Hour, 5432); err != nil {
		t.Fatal(err)
	}
	inst := m.live[owner.InstanceID]
	newer := inst.resolvedEgress[address]
	m.appResolved["app"][address] = time.Now().Add(5 * time.Minute)
	m.appDatabaseResolved["app"][databaseEgressKey{address, 5432}] = time.Now().Add(5 * time.Minute)
	for i := 0; i < api.DatabaseEgressInstanceFamilyMax+10; i++ {
		a := netip.AddrFrom4([4]byte{198, 51, byte(i >> 8), byte(i)})
		m.appDatabaseResolved["app"][databaseEgressKey{a, 5432}] = time.Now().Add(5 * time.Minute)
	}
	m.seedResolvedEgress(t.Context(), owner.InstanceID)
	if !inst.resolvedEgress[address].Equal(newer) || !inst.databaseEgress[databaseEgressKey{address, 5432}].Equal(newer) {
		t.Fatal("seed shortened an existing grant")
	}
	if len(inst.databaseEgress) != api.DatabaseEgressInstanceFamilyMax {
		t.Fatalf("database seed exceeded capacity: %d", len(inst.databaseEgress))
	}
}

func TestTimedEgressLegacyRunnerUsesOneAtomicProgram(t *testing.T) {
	r := &fakeRunner{}
	m := newTestManager(r, &fakeVMM{})
	source := wakeGated(t, m, "legacy-renew", "app")
	before := len(r.commands)
	if err := m.AllowResolvedEgress(t.Context(), source, resolvedOwner(t, m, source), []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}, time.Hour, 5432); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != before+1 {
		t.Fatalf("renewal split across %d processes", len(r.commands)-before)
	}
	program := strings.Join(r.commands[before], " ")
	if strings.Count(program, "delete element") != 4 || strings.Contains(program, "timeout 3599s } ; delete element ip faas egress_resolved { 203.0.113.10 timeout") {
		t.Fatalf("invalid atomic renewal: %s", program)
	}
}
