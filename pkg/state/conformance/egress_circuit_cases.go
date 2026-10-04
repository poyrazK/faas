// adr: 531
package conformance

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

func testEgressCircuitDesiredState(t *testing.T, fx *Fixture) {
	v4 := netns.EgressCircuitTarget{Addr: netip.MustParseAddr("203.0.113.9"), Port: 5432}
	v6 := netns.EgressCircuitTarget{Addr: netip.MustParseAddr("2001:db8::1"), Port: 5432}
	if _, memory := fx.Store.(*state.MemStore); memory {
		// ADR-098 explicitly refuses durable upstream policy in MemStore.
		// Pin the errors and empty results rather than accepting success.
		get, getErr := fx.Store.GetAppEgressCircuits(fx.Ctx, fx.App.ID)
		put, putErr := fx.Store.PutAppEgressCircuits(fx.Ctx, fx.App.ID, []netns.EgressCircuitTarget{v4})
		ids, listErr := fx.Store.ListAppEgressCircuitAppIDs(fx.Ctx)
		for _, err := range []error{getErr, putErr, listErr} {
			if err == nil || !strings.Contains(err.Error(), "MemStore does not implement ADR-098") {
				t.Fatalf("memory durable-circuit refusal: %v", err)
			}
		}
		if get.Revision != 0 || put.Revision != 0 || len(get.Targets) != 0 || len(put.Targets) != 0 || len(ids) != 0 {
			t.Fatal("unsupported memory policy returned a desired set")
		}
		return
	}
	before, err := fx.Store.GetAppEgressCircuits(fx.Ctx, fx.App.ID)
	if err != nil || before.Revision != 0 || len(before.Targets) != 0 {
		t.Fatalf("initial desired set: %+v/%v", before, err)
	}
	first, err := fx.Store.PutAppEgressCircuits(fx.Ctx, fx.App.ID, []netns.EgressCircuitTarget{v6, v4, v4})
	if err != nil || first.Revision != 1 || !reflect.DeepEqual(first.Targets, []netns.EgressCircuitTarget{v4, v6}) {
		t.Fatalf("canonical desired set: %+v/%v", first, err)
	}
	same, err := fx.Store.PutAppEgressCircuits(fx.Ctx, fx.App.ID, []netns.EgressCircuitTarget{v4, v6})
	if err != nil || same.Revision != 1 {
		t.Fatalf("equivalent desired set advanced revision: %+v/%v", same, err)
	}
	if _, err := fx.Store.PutAppEgressCircuits(fx.Ctx, fx.App.ID, []netns.EgressCircuitTarget{{Port: 0}}); err == nil {
		t.Fatal("invalid circuit persisted")
	}
	read, err := fx.Store.GetAppEgressCircuits(fx.Ctx, fx.App.ID)
	if err != nil || !reflect.DeepEqual(read, first) {
		t.Fatalf("invalid mutation changed desired set: %+v/%v", read, err)
	}
	cleared, err := fx.Store.PutAppEgressCircuits(fx.Ctx, fx.App.ID, nil)
	if err != nil || cleared.Revision != 2 || len(cleared.Targets) != 0 {
		t.Fatalf("closed desired set: %+v/%v", cleared, err)
	}
	ids, err := fx.Store.ListAppEgressCircuitAppIDs(fx.Ctx)
	if err != nil || len(ids) != 1 || ids[0] != fx.App.ID {
		t.Fatalf("closed set lost reconciliation subject: %v/%v", ids, err)
	}
}
