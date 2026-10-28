package state_test

// adr: 20261028131500

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublishedEventClaimRenewal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedReplayPreviewApp(t, s)
	if _, _, err := s.UpsertEventSubscription(ctx, account, app, "orders", "invoice.paid", nil); err != nil {
		t.Fatal(err)
	}
	envelope, err := (events.Envelope{ID: "evt-renew", Source: "orders", Type: "invoice.paid", Data: json.RawMessage(`{}`)}).Normalize(account, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(ctx, "apid", "event.published", &account, payload); err != nil {
		t.Fatal(err)
	}
	// Claim after the receipt was accepted, so it is due.
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	receipt, err := s.ClaimDuePublishedEventForRecipientAdoption(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	lease := func(query string, args ...any) time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, query, args...).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at.UTC()
	}
	receiptLease := func() time.Time {
		return lease(`SELECT lease_until FROM event_fanout_outbox WHERE id=$1`, receipt.ID)
	}
	if got := receiptLease(); !got.Equal(now.Add(state.PublishedEventLease)) {
		t.Fatalf("receipt lease = %s, want claim time + %s", got, state.PublishedEventLease)
	}
	later := now.Add(50 * time.Second)
	stale := *receipt
	stale.ClaimToken = uuid.NewString()
	if err := s.RenewPublishedEventClaims(ctx, []*state.PublishedEventWork{&stale}, later); err != nil {
		t.Fatal(err)
	}
	if got := receiptLease(); !got.Equal(now.Add(state.PublishedEventLease)) {
		t.Fatalf("a claim with another token renewed the lease to %s", got)
	}
	if err := s.RenewPublishedEventClaims(ctx, []*state.PublishedEventWork{receipt}, later); err != nil {
		t.Fatal(err)
	}
	if got := receiptLease(); !got.Equal(later.Add(state.PublishedEventLease)) {
		t.Fatalf("renewed receipt lease = %s, want %s", got, later.Add(state.PublishedEventLease))
	}

	if err := s.InitializePublishedEventRecipients(ctx, receipt, now); err != nil {
		t.Fatal(err)
	}
	recipient, err := s.ClaimDuePublishedEventRecipient(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenewPublishedEventRecipientClaims(ctx, []*state.PublishedEventRecipientWork{recipient}, later); err != nil {
		t.Fatal(err)
	}
	got := lease(`SELECT lease_until FROM event_fanout_recipients WHERE outbox_id=$1 AND subscription_id=$2`, recipient.OutboxID, recipient.Recipient.ID)
	if !got.Equal(later.Add(state.PublishedEventLease)) {
		t.Fatalf("renewed recipient lease = %s, want %s", got, later.Add(state.PublishedEventLease))
	}
}

func TestMemPublishedEventClaimRenewal(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, app := seedReplayPreviewApp(t, m)
	if _, _, err := m.UpsertEventSubscription(ctx, account, app, "orders", "invoice.paid", nil); err != nil {
		t.Fatal(err)
	}
	envelope, err := (events.Envelope{ID: "evt-renew", Source: "orders", Type: "invoice.paid", Data: json.RawMessage(`{}`)}).Normalize(account, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AppendEvent(ctx, "apid", "event.published", &account, payload); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	receipt, err := m.ClaimDuePublishedEventForRecipientAdoption(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// Renewed just before it would lapse, the claim outlives its first lease.
	if err := m.RenewPublishedEventClaims(ctx, []*state.PublishedEventWork{receipt}, now.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimDuePublishedEventForRecipientAdoption(ctx, now.Add(state.PublishedEventLease+time.Second)); err == nil {
		t.Fatal("a renewed receipt claim was taken by another worker")
	}
	if _, err := m.ClaimDuePublishedEventForRecipientAdoption(ctx, now.Add(50*time.Second+state.PublishedEventLease+time.Second)); err != nil {
		t.Fatalf("a lapsed receipt claim was not reclaimable: %v", err)
	}
}
