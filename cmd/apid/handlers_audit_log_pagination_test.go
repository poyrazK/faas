package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseAuditLogQueryBeforeCursor(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000042")
	at := time.Date(2026, 9, 11, 12, 34, 56, 123456789, time.UTC)
	encoded := cursor.Encode(cursor.Key{CreatedAt: at, ID: id.String()})
	req := httptest.NewRequest("GET", "/v1/audit-log?before="+url.QueryEscape(encoded), nil)

	_, _, before, limit, ok := parseAuditLogQuery(httptest.NewRecorder(), req)
	if !ok {
		t.Fatal("parseAuditLogQuery rejected a cursor emitted by cursor.Encode")
	}
	if limit != listAuditLogLimitDefault {
		t.Fatalf("limit = %d, want %d", limit, listAuditLogLimitDefault)
	}
	if before == nil || !before.ReceivedAt.Equal(at) || before.ID != id {
		t.Fatalf("before = %+v, want timestamp %s and id %s", before, at, id)
	}
}

func TestParseAuditLogQueryRejectsMalformedBeforeCursor(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/audit-log?before=not-a-cursor", nil)
	recorder := httptest.NewRecorder()

	_, _, before, _, ok := parseAuditLogQuery(recorder, req)
	if ok || before != nil {
		t.Fatalf("parseAuditLogQuery accepted malformed cursor: before=%+v ok=%v", before, ok)
	}
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var problem api.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeValidation {
		t.Fatalf("problem code = %q, want %q", problem.Code, api.CodeValidation)
	}
}

func TestWriteListAuditLogResponseEmitsCompoundNextCursor(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 34, 56, 123456789, time.UTC)
	firstID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	secondID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	rows := []state.AuditLog{
		{ID: firstID, Kind: "account.deleted", ReceivedAt: at},
		{ID: secondID, Kind: "account.deleted", ReceivedAt: at.Add(-time.Second)},
	}
	recorder := httptest.NewRecorder()

	writeListAuditLogResponse(recorder, rows, 1)
	var response api.ListAuditLogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Entries) != 1 || response.Entries[0].ID != firstID.String() {
		t.Fatalf("entries = %+v, want first row only", response.Entries)
	}
	if response.NextBefore == "" {
		t.Fatal("next_before is empty despite an over-read row")
	}
	key, err := cursor.Decode(response.NextBefore)
	if err != nil {
		t.Fatalf("decode next_before: %v", err)
	}
	if key.ID != firstID.String() || !key.CreatedAt.Equal(at) {
		t.Fatalf("next_before = %+v, want last returned row", key)
	}
}

func TestWriteListAuditLogResponseOmitsTerminalCursor(t *testing.T) {
	row := state.AuditLog{
		ID:         uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Kind:       "account.deleted",
		ReceivedAt: time.Date(2026, 9, 11, 12, 34, 56, 0, time.UTC),
	}
	recorder := httptest.NewRecorder()

	writeListAuditLogResponse(recorder, []state.AuditLog{row}, 1)
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := response["next_before"]; ok {
		t.Fatalf("terminal response unexpectedly contains next_before: %v", response["next_before"])
	}
}
