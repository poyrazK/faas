//go:build linux && metal

// adr: 460 — mixed exact-policy spares preserve kernel identity and capacity.
// spec: §6.3
package fcvm

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Use the existing private mount/network namespace harness. This test prepares
// and claims unused networks; the restore and leak gates remain required too.
func TestMetalPreparedNetworkMixedPolicies(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private mount and network namespaces")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	if err := run.Run(ctx, []string{"ip", "link", "add", netns.TenantBridge, "type", "bridge"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	for _, argv := range [][]string{
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	m, p := testPreparedPool(t, 3)
	m.run = run
	p.move, p.removed = movePreparedNetns, preparedNetworkRemoved
	a := fillTestPreparedPool(t, m, p, 100)
	if len(p.ready) != 3 {
		t.Fatal("initial cache did not fill")
	}
	oldest := p.ready[0].config.Netns
	p.ready[0].created = time.Now().Add(-time.Second)
	b := fillTestPreparedPool(t, m, p, 250)
	if _, err := os.Stat("/run/netns/" + oldest); !os.IsNotExist(err) {
		t.Fatal("oldest namespace survived replacement")
	}
	if len(p.ready) != 3 || len(m.alloc.reserved) != 3 || m.LeasedCount() != 0 {
		t.Fatal("policy change altered global unused-network capacity")
	}
	seenSlots, seenUIDs := map[int]bool{}, map[int]bool{}
	for i, policy := range []preparedNetworkPolicy{a, b} {
		var old preparedNetworkEntry
		for _, entry := range p.ready {
			if entry.policy == policy {
				old = entry
				break
			}
		}
		if old.lease.Instance == "" {
			t.Fatal("policy spare missing")
		}
		before, err := os.Stat("/run/netns/" + old.config.Netns)
		if err != nil {
			t.Fatal(err)
		}
		entry := p.claim(fmt.Sprintf("optmixed%d", i), policy)
		if entry == nil {
			t.Fatal("exact-policy claim failed")
		}
		t.Cleanup(func() { p.discard(*entry) })
		if seenSlots[entry.lease.Slot] || seenUIDs[entry.lease.UID] {
			t.Fatal("mixed claims shared slot or UID")
		}
		seenSlots[entry.lease.Slot], seenUIDs[entry.lease.UID] = true, true
		requested := entry.config
		requested.GuestAppPort = netns.AppPort
		if hit, err := m.setupWakeNetwork(ctx, requested, entry); !hit || err != nil {
			t.Fatalf("exact-policy setup rebuilt namespace: hit=%v err=%v", hit, err)
		}
		after, err := os.Stat("/run/netns/" + entry.config.Netns)
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("claim/setup replaced kernel namespace identity")
		}
		if _, err := os.Stat("/run/netns/" + old.config.Netns); !os.IsNotExist(err) {
			t.Fatal("previous namespace alias survived claim")
		}
		assertBatchNetwork(t, entry.config)
	}
	if m.LeasedCount() != 2 || len(m.alloc.reserved) != 1 {
		t.Fatal("mixed claims changed lease/reservation accounting")
	}
}
