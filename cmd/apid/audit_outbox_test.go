package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDrainAuditOutboxOnceDeliversMissedNotification(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	payload := []byte(`{"kind":"app.signature_invalid","app_id":"app-1","deployment_id":"dep-1","ref":"registry/app@sha256:1","signer":""}`)
	if _, err := store.EnqueueAuditEvent(ctx, "apid", "app.signature_invalid", nil, payload, "signature:dep-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	platform := events.NewPlatform("apid", store, log, nil, nil)

	count, err := drainAuditOutboxOnce(ctx, store, log, platform)
	if err != nil || count != 1 {
		t.Fatalf("drain = (%d, %v), want (1, nil)", count, err)
	}
	count, err = drainAuditOutboxOnce(ctx, store, log, platform)
	if err != nil || count != 0 {
		t.Fatalf("second drain = (%d, %v), want (0, nil)", count, err)
	}

	rows, err := store.ListEvents(ctx, "", 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("events length = %d, want audit + typed projection", len(rows))
	}
	seenAudit, seenProjection := false, false
	for _, row := range rows {
		if row.Actor == "apid" && row.Kind == "app.signature_invalid" {
			seenAudit = true
		}
		if row.Actor == "apid" && row.Kind == events.WakeDeployFailed {
			seenProjection = true
		}
	}
	if !seenAudit || !seenProjection {
		t.Fatalf("events missing durable audit or typed projection: %+v", rows)
	}
}
