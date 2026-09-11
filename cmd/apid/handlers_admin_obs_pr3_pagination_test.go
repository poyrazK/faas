package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObsAuditLogSearchPaginatesWithCompoundCursor(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	accountID := uuid.MustParse(e.acct.ID)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	for _, id := range ids {
		if err := e.store.InsertAuditLog(context.Background(), state.AuditLog{
			ID:         id,
			Kind:       "account.updated",
			AccountID:  &accountID,
			ReceivedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}

	first := e.do(t, "GET", "/v1/admin/obs/audit-log/search?limit=2", nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first page: got %d, body=%s", first.Code, first.Body.String())
	}
	var firstBody api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if len(firstBody.Items) != 2 || firstBody.NextBefore == "" {
		t.Fatalf("first page: items=%d next_before=%q", len(firstBody.Items), firstBody.NextBefore)
	}
	if firstBody.Items[0].ID != ids[1].String() || firstBody.Items[1].ID != ids[2].String() {
		t.Fatalf("first page order: got %q, %q", firstBody.Items[0].ID, firstBody.Items[1].ID)
	}

	second := e.do(t, "GET", "/v1/admin/obs/audit-log/search?limit=2&before="+firstBody.NextBefore, nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page: got %d, body=%s", second.Code, second.Body.String())
	}
	var secondBody api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatal(err)
	}
	if len(secondBody.Items) != 1 || secondBody.Items[0].ID != ids[0].String() {
		t.Fatalf("second page: got %+v", secondBody.Items)
	}
	if secondBody.NextBefore != "" {
		t.Fatalf("terminal page emitted next_before=%q", secondBody.NextBefore)
	}
}

func TestObsAuditLogSearchRejectsMalformedBeforeCursor(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?before=not-a-cursor", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed before: got %d, want 400", rec.Code)
	}
}
