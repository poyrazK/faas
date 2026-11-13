package support

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTicketSnapshotFirstWinsAndTicketCustomer(t *testing.T) {
	f := supportDB(t)
	ctx := t.Context()
	ticket := f.create(t, "deploys keep failing")

	customer, err := f.s.TicketCustomer(ctx, ticket)
	if err != nil || customer.AccountID != f.customer.ID || customer.OrgID != "" {
		t.Fatalf("customer = %+v, %v; want personal owner %s", customer, err, f.customer.ID)
	}
	if missing, err := f.s.TicketCustomer(ctx, newID()); err != nil || missing.AccountID != "" {
		t.Fatalf("unknown ticket = %+v, %v", missing, err)
	}

	if _, _, ok, err := f.s.TicketSnapshot(ctx, ticket); err != nil || ok {
		t.Fatalf("snapshot before save: ok=%v err=%v", ok, err)
	}
	first := time.Now().UTC().Truncate(time.Millisecond)
	if err := f.s.SaveTicketSnapshot(ctx, ticket, first, json.RawMessage(`{"plan":"pro"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SaveTicketSnapshot(ctx, ticket, first.Add(time.Minute), json.RawMessage(`{"plan":"scale"}`)); err != nil {
		t.Fatal(err)
	}
	raw, at, ok, err := f.s.TicketSnapshot(ctx, ticket)
	if err != nil || !ok || !strings.Contains(string(raw), `"pro"`) || !at.Equal(first) {
		t.Fatalf("snapshot = %s at %v ok=%v err=%v; want the first save", raw, at, ok, err)
	}
	if err := f.s.SaveTicketSnapshot(ctx, ticket, first, json.RawMessage(`{"x":"`+strings.Repeat("a", SnapshotMaxBytes)+`"}`)); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("oversized snapshot err = %v", err)
	}
	if _, err := f.pool.Exec(ctx, "DELETE FROM support_tickets WHERE id=$1", ticket); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := f.s.TicketSnapshot(ctx, ticket); err != nil || ok {
		t.Fatalf("snapshot survived ticket delete: ok=%v err=%v", ok, err)
	}
}
