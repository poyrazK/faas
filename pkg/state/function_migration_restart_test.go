package state

// adr: 1578
// Native consumption is simulated; PostgreSQL transactions are real in Pg.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type functionRestartTestStore interface {
	functionDestinationTestStore
	FunctionMigrationRestartStore
}

func functionDestinationRestartDiscovery(t *testing.T, s functionRestartTestStore, reopen func() functionRestartTestStore, expired bool) {
	t.Helper()
	ctx := t.Context()
	ins, g, r, input := functionDestinationFixture(t, s)
	list := func(store functionRestartTestStore, node, after string) []FunctionMigrationDestination {
		t.Helper()
		rows, err := store.ListRestartFunctionMigrationDestinations(ctx, node, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if len(list(s, g.Binding.NodeID, "")) != 0 {
		t.Fatal("startup attempted an issued restore")
	}
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal(err)
	}
	s = reopen()
	rows := list(s, g.Binding.NodeID, "")
	if len(rows) != 1 || !rows[0].Grant.Equal(g) || rows[0].Receipt == nil || !rows[0].Receipt.Equal(r) {
		t.Fatal("fresh store lost the ready attempt")
	}
	if len(list(s, g.Source.Parent.Binding.NodeID, "")) != 0 || len(list(s, g.Binding.NodeID, ins.ID)) != 0 {
		t.Fatal("startup scan crossed node/cursor scope")
	}
	rows[0].Grant.Binding.Token = uuid.NewString()
	if !list(s, g.Binding.NodeID, "")[0].Grant.Equal(g) {
		t.Fatal("caller mutated durable readiness")
	}
	if expired {
		if err := s.RenewMigrationLease(ctx, g.Source.LeaseToken, ins.NodeID, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		if len(list(reopen(), g.Binding.NodeID, "")) != 0 {
			t.Fatal("startup attempted an expired uncommitted destination")
		}
		return
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewMigrationLease(ctx, g.Source.LeaseToken, ins.NodeID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	rows = list(reopen(), g.Binding.NodeID, "")
	if len(rows) != 1 || rows[0].Phase != FunctionDestinationCommitted {
		t.Fatal("committed cleanup was lost after lease expiry")
	}
	if err := s.DeleteMigrationLease(ctx, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if len(list(reopen(), g.Binding.NodeID, "")) != 0 {
		t.Fatal("completed cleanup remains discoverable")
	}
	for _, args := range []struct {
		node, after string
		limit       int
	}{{"", "", 1}, {uuid.Nil.String(), "", 1}, {g.Binding.NodeID, "bad", 1}, {g.Binding.NodeID, "", 0}} {
		if _, err := s.ListRestartFunctionMigrationDestinations(ctx, args.node, args.after, args.limit); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("invalid restart scan accepted", err)
		}
	}
}

func TestFunctionDestinationRestartMem(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed cleanup", true: "expired readiness"}[expired], func(t *testing.T) {
			s := NewMemStore()
			functionDestinationRestartDiscovery(t, s, func() functionRestartTestStore { return s }, expired)
		})
	}
}
