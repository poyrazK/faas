// adr: 375
package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

type securityRefreshInvalidator struct {
	fakeInvalidator
	refreshes int
}

func (i *securityRefreshInvalidator) RequestTrafficRevocationRefresh() { i.refreshes++ }

func TestSecurityNotificationRequestsAuthoritativeReread(t *testing.T) {
	inv := &securityRefreshInvalidator{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, raw := range []string{`{}`, `{"scope_kind":"app","scope_id":"12345678-1234-1234-1234-123456789abc","revision":0}`} {
		handleInvalidation(t.Context(), inv, db.Notification{Channel: db.NotifyTrafficSecurityChanged, Payload: raw}, log)
	}
	if inv.refreshes != 0 {
		t.Fatal("malformed event requested security refresh")
	}
	handleInvalidation(t.Context(), inv, db.Notification{Channel: db.NotifyTrafficSecurityChanged, Payload: `{"scope_kind":"app","scope_id":"12345678-1234-1234-1234-123456789abc","revision":1}`}, log)
	if inv.refreshes != 1 {
		t.Fatalf("valid notification refreshes=%d", inv.refreshes)
	}
	if _, err := newTrafficRevocationRegistry(t.Context(), nil); err == nil {
		t.Fatal("missing production store passed startup verification")
	}
}
