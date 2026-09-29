// adr: 375
package state_test

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreEgressCircuitDesiredRevisionAndRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	ctx := t.Context()
	appID := seedAppForAllowlist(t, ctx, s, "circuit-durable")
	before, err := s.GetAppEgressCircuits(ctx, appID)
	if err != nil || before.Revision != 0 || len(before.Targets) != 0 {
		t.Fatalf("new app policy=%v error=%v", before, err)
	}
	v4 := netns.EgressCircuitTarget{Addr: netip.MustParseAddr("203.0.113.9"), Port: 5432}
	v6 := netns.EgressCircuitTarget{Addr: netip.MustParseAddr("2001:db8::1"), Port: 5432}
	first, err := s.PutAppEgressCircuits(ctx, appID, []netns.EgressCircuitTarget{v6, v4, v4})
	if err != nil || first.Revision != 1 || !reflect.DeepEqual(first.Targets, []netns.EgressCircuitTarget{v4, v6}) {
		t.Fatalf("first policy=%v error=%v", first, err)
	}
	same, err := s.PutAppEgressCircuits(ctx, appID, []netns.EgressCircuitTarget{v4, v6})
	if err != nil || same.Revision != first.Revision {
		t.Fatalf("unchanged policy advanced revision: %v %v", same, err)
	}
	// A fresh store models daemon restart: no in-memory desired policy remains.
	restarted := state.NewPgStore(pool)
	read, err := restarted.GetAppEgressCircuits(ctx, appID)
	if err != nil || !reflect.DeepEqual(read, first) {
		t.Fatalf("restart lost desired policy: %v %v", read, err)
	}
	if _, err := s.PutAppEgressCircuits(ctx, appID, []netns.EgressCircuitTarget{{Port: 0}}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	cleared, err := s.PutAppEgressCircuits(ctx, appID, nil)
	if err != nil || cleared.Revision != 2 || len(cleared.Targets) != 0 {
		t.Fatalf("close policy=%v error=%v", cleared, err)
	}
	apps, err := restarted.ListAppEgressCircuitAppIDs(ctx)
	if err != nil || len(apps) != 1 || apps[0] != appID {
		t.Fatalf("closed policy lost reconciliation subject: %v %v", apps, err)
	}
}
