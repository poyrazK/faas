package conformance

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/auditanchor"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026163015: audit rows page in (received_at, id) order within a
// window, anchors append once per seq, and a chain sealed through the store
// verifies through it: the stored text the digest reads is stable.
func testAuditLogAnchors(t *testing.T, fx *Fixture) {
	s, ok := fx.Store.(state.AuditLogAnchorStore)
	if !ok {
		t.Fatalf("%T does not keep audit log anchors", fx.Store)
	}
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	ids := []uuid.UUID{
		uuid.MustParse("10000000-0000-4000-8000-000000000001"),
		uuid.MustParse("20000000-0000-4000-8000-000000000002"),
		uuid.MustParse("30000000-0000-4000-8000-000000000003"),
	}
	account := uuid.MustParse(fx.Account.ID)
	for i, entry := range []state.AuditLog{
		{ID: ids[2], Kind: "conformance.a", AccountID: &account, AccountEmail: "a@example.com", Actor: "apid", ReceivedAt: base.Add(2 * time.Minute), Data: json.RawMessage(`{"b": [2], "a": 1}`)},
		{ID: ids[0], Kind: "conformance.b", ReceivedAt: base.Add(2 * time.Minute)},
		{ID: ids[1], Kind: "conformance.c", ReceivedAt: base.Add(time.Minute), Data: json.RawMessage(`{"n":1}`)},
	} {
		if err := fx.Store.InsertAuditLog(fx.Ctx, entry); err != nil {
			t.Fatalf("insert audit row %d: %v", i, err)
		}
	}

	window := func(after *state.AuditLog, limit int) []state.AuditLog {
		t.Helper()
		rows, err := s.AuditLogWindowPage(fx.Ctx, base, base.Add(10*time.Minute), after, limit)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	first := window(nil, 2)
	if len(first) != 2 || first[0].ID != ids[1] || first[1].ID != ids[0] || len(first[1].Data) != 0 || first[1].AccountID != nil {
		t.Fatalf("first page = %+v, want the earliest row, then the lower id of the two at one time (no data)", first)
	}
	rest := window(&first[1], 2)
	if len(rest) != 1 || rest[0].ID != ids[2] || rest[0].AccountID == nil || *rest[0].AccountID != account ||
		rest[0].AccountEmail != "a@example.com" || !rest[0].ReceivedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("second page = %+v", rest)
	}
	if len(window(&rest[0], 2)) != 0 {
		t.Fatal("a page after the last row is not empty")
	}

	key, err := auditanchor.DeriveKey(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	one, ok, err := auditanchor.Seal(fx.Ctx, s, key, base.Add(30*time.Minute), auditanchor.SealOptions{})
	if err != nil || !ok || one.Seq != 1 || one.RowCount < 3 {
		t.Fatalf("first seal = %+v, %v, %v", one, ok, err)
	}
	if inserted, err := s.InsertAuditLogAnchor(fx.Ctx, one); err != nil || inserted {
		t.Fatalf("second anchor with seq 1 = %v, %v; want nothing inserted", inserted, err)
	}
	if err := fx.Store.InsertAuditLog(fx.Ctx, state.AuditLog{ID: uuid.New(), Kind: "conformance.d", ReceivedAt: base.Add(25 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	two, ok, err := auditanchor.Seal(fx.Ctx, s, key, base.Add(50*time.Minute), auditanchor.SealOptions{})
	if err != nil || !ok || two.Seq != 2 || two.RowCount != 1 || !two.WindowStart.Equal(one.WindowEnd) {
		t.Fatalf("second seal = %+v, %v, %v", two, ok, err)
	}
	if head, ok, err := s.LatestAuditLogAnchor(fx.Ctx); err != nil || !ok || head.Seq != 2 || head.MAC != two.MAC || head.CreatedAt.IsZero() {
		t.Fatalf("latest anchor = %+v, %v, %v", head, ok, err)
	}
	if list, err := s.ListAuditLogAnchors(fx.Ctx, 2, 10); err != nil || len(list) != 1 || list[0].Seq != 2 {
		t.Fatalf("anchors from seq 2 = %+v, %v", list, err)
	}
	if seq, ok, err := s.FirstAuditLogAnchorEndingAfter(fx.Ctx, one.WindowEnd); err != nil || !ok || seq != 2 {
		t.Fatalf("first anchor ending after anchor 1 = %d, %v, %v; want 2", seq, ok, err)
	}
	report, err := auditanchor.Verify(fx.Ctx, s, key, 0)
	if err != nil || len(report.Findings) != 0 || report.Anchors != 2 || report.LastSeq != 2 {
		t.Fatalf("verify = %+v, %v", report, err)
	}
}
