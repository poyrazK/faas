package imaged

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEmitSignatureAuditUsesDurableOutbox(t *testing.T) {
	ctx := context.Background()
	h := newTestHandler(t)
	notif := &fakeNotifier{}
	h.notif = notif
	app := state.App{ID: "app-1", Slug: "demo"}
	dep := state.Deployment{ID: "dep-1"}

	h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, "registry/app@sha256:1", "")
	h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, "registry/app@sha256:1", "")

	outbox, ok := h.store.(state.AuditEventOutboxStore)
	if !ok {
		t.Fatal("test store does not implement durable audit outbox")
	}
	item, err := outbox.ClaimAuditEvent(ctx, "test", time.Minute)
	if err != nil {
		t.Fatalf("claim durable audit event: %v", err)
	}
	if item.Kind != "app.signature_invalid" || item.DedupeKey != "imaged.signature.app.signature_invalid.dep-1" {
		t.Fatalf("outbox item = %+v, want signature kind and deployment dedupe key", item)
	}
	if item.Actor != "apid" {
		t.Fatalf("outbox actor = %q, want apid audit attribution", item.Actor)
	}
	if len(notif.calls) != 2 {
		t.Fatalf("notifications = %d, want one per producer call", len(notif.calls))
	}
	var p signatureAuditPayload
	if err := json.Unmarshal([]byte(notif.calls[0].payload), &p); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if p.OutboxID != item.ID {
		t.Fatalf("notification outbox_id = %d, want %d", p.OutboxID, item.ID)
	}
}
