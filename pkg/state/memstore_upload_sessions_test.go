// Package state — MemStore upload-session method tests.
//
// Why this file exists: pkg/state coverage floor is 70%
// (Makefile::check-state-coverage). PR #1204 added 13 MemStore
// methods backing the resumable-upload Store interface
// (cmd/apid/handlers_upload_session.go) — without a test that
// exercises them, the floor fails on this PR alone (the cmd/apid
// tests use the MemStore but go test pkg/state/coverage counts
// only pkg/state/*.go against pkg/state tests).
//
// The tests are end-to-end whitebox: they build an upload session
// (CreateUploadSession), run the atomic CAS (AppendUploadBytes),
// transition through every terminal state (MarkUploadSessionCommitted
// / CancelUploadSession / ExpireUploadSession), then verify the
// reaper sweeps (ReapExpiredUploadSessions / ReapStaleUploadPartFiles)
// and the dedupe companion (RecordUploadCommitOutcome /
// GetUploadCommitOutcome) round-trip correctly. The cap queries
// (CountOpenUploadSessionsByAccountApp / SumOpenUploadSessionBytesByAccount)
// are also exercised so every new method has at least one hit.
//
// Test design mirrors pkg/state/memstore_alert_presets_test.go
// (seed → exercise → assert). No live Postgres needed — the
// MemStore is the test fixture by design.
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// accountUUIDFromString is the test-side helper that mirrors
// pgtypeFromUUIDString at cmd/apid/handlers_app_errors_projection.go:171.
// Tests don't go through the handler so they need their own copy.
func accountUUIDFromString(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid %q: %v", s, err)
	}
	return u
}

// seedUploadSession inserts an open row directly into the memstore
// and returns the row + a clean MemStore for re-use. Avoids the
// public CreateUploadSession signature (which doesn't let us set
// expires_at to "already past" for the reaper test).
func seedUploadSession(t *testing.T, m *MemStore, opts struct {
	id           string
	account      string
	slug         string
	totalSize    int64
	received     int64
	status       string
	expiresIn    time.Duration
	lastPatched  time.Duration
}) sqlc.UploadSession {
	t.Helper()
	now := time.Now().UTC()
	row := sqlc.UploadSession{
		ID:            opts.id,
		AccountID:     accountUUIDFromString(t, opts.account),
		AppSlug:       opts.slug,
		TotalSize:     opts.totalSize,
		ReceivedBytes: opts.received,
		ChunkSize:     8 * 1024 * 1024,
		Sha256Hex:     pgtype.Text{String: "", Valid: false},
		PartPath:      "/tmp/" + opts.id + ".part",
		Status:        opts.status,
		CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		LastPatchedAt: pgtype.Timestamptz{Time: now.Add(opts.lastPatched), Valid: true},
		ExpiresAt:     pgtype.Timestamptz{Time: now.Add(opts.expiresIn), Valid: true},
		DeploymentID:  pgtype.Text{String: "", Valid: false},
	}
	m.mu.Lock()
	m.uploadSessions[row.ID] = row
	m.mu.Unlock()
	return row
}

// TestMemStoreUploadSession_Lifecycle exercises CreateUploadSession,
// GetUploadSession, AppendUploadBytes (happy path + offset mismatch),
// MarkUploadSessionCommitted, CancelUploadSession, ExpireUploadSession,
// and the cap queries. Reaper + dedupe tests live below so a
// per-method failure points at one method.
func TestMemStoreUploadSession_Lifecycle(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := accountUUIDFromString(t, "11111111-1111-1111-1111-111111111111")

	// 1. CreateUploadSession → row exists, status='open', received=0.
	created, err := m.CreateUploadSession(ctx, sqlc.CreateUploadSessionParams{
		ID:        "sess-create",
		AccountID: acct,
		AppSlug:   "myapp",
		TotalSize: 1024,
		ChunkSize: 4096,
		Sha256Hex: pgtype.Text{String: "", Valid: false},
		PartPath:  "/tmp/sess-create.part",
	})
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	if created.Status != "open" {
		t.Errorf("Status = %q; want open", created.Status)
	}
	if created.ReceivedBytes != 0 {
		t.Errorf("ReceivedBytes = %d; want 0", created.ReceivedBytes)
	}

	// 2. GetUploadSession → round-trip.
	got, err := m.GetUploadSession(ctx, "sess-create")
	if err != nil {
		t.Fatalf("GetUploadSession: %v", err)
	}
	if got.ID != "sess-create" {
		t.Errorf("GetUploadSession.ID = %q; want sess-create", got.ID)
	}
	if _, err := m.GetUploadSession(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUploadSession(missing) err = %v; want ErrNotFound", err)
	}

	// 3. AppendUploadBytes happy path: row.ReceivedBytes=0 → new=512.
	appendRow, err := m.AppendUploadBytes(ctx, sqlc.AppendUploadBytesParams{
		ID:              "sess-create",
		ReceivedBytes:   512,
		ReceivedBytes_2: 0,
	})
	if err != nil {
		t.Fatalf("AppendUploadBytes happy: %v", err)
	}
	if appendRow.ReceivedBytes != 512 {
		t.Errorf("ReceivedBytes after append = %d; want 512", appendRow.ReceivedBytes)
	}

	// 4. AppendUploadBytes offset mismatch: expected=0 but row=512 → ErrConflict.
	if _, err := m.AppendUploadBytes(ctx, sqlc.AppendUploadBytesParams{
		ID:              "sess-create",
		ReceivedBytes:   1024,
		ReceivedBytes_2: 0,
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("AppendUploadBytes mismatch err = %v; want ErrConflict", err)
	}

	// 5. AppendUploadBytes missing row → ErrNotFound.
	if _, err := m.AppendUploadBytes(ctx, sqlc.AppendUploadBytesParams{
		ID:              "missing",
		ReceivedBytes:   0,
		ReceivedBytes_2: 0,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendUploadBytes(missing) err = %v; want ErrNotFound", err)
	}

	// 6. CountOpenUploadSessionsByAccountApp → 1 open for (acct, myapp).
	if n, err := m.CountOpenUploadSessionsByAccountApp(ctx, sqlc.CountOpenUploadSessionsByAccountAppParams{
		Column1: acct,
		AppSlug: "myapp",
	}); err != nil {
		t.Fatalf("CountOpenUploadSessionsByAccountApp: %v", err)
	} else if n != 1 {
		t.Errorf("count = %d; want 1", n)
	}

	// 7. SumOpenUploadSessionBytesByAccount → total=1024 for acct.
	if total, err := m.SumOpenUploadSessionBytesByAccount(ctx, acct); err != nil {
		t.Fatalf("SumOpenUploadSessionBytesByAccount: %v", err)
	} else if total != 1024 {
		t.Errorf("sum = %d; want 1024", total)
	}

	// 8. MarkUploadSessionCommitted: open → committed, deployment_id set.
	if _, err := m.MarkUploadSessionCommitted(ctx, sqlc.MarkUploadSessionCommittedParams{
		ID:           "sess-create",
		DeploymentID: pgtype.Text{String: "dep-1", Valid: true},
	}); err != nil {
		t.Fatalf("MarkUploadSessionCommitted: %v", err)
	}
	row, _ := m.GetUploadSession(ctx, "sess-create")
	if row.Status != "committed" {
		t.Errorf("Status after commit = %q; want committed", row.Status)
	}
	if !row.DeploymentID.Valid || row.DeploymentID.String != "dep-1" {
		t.Errorf("DeploymentID = %+v; want {dep-1 true}", row.DeploymentID)
	}

	// 9. MarkUploadSessionCommitted on already-committed row → ErrConflict.
	if _, err := m.MarkUploadSessionCommitted(ctx, sqlc.MarkUploadSessionCommittedParams{
		ID:           "sess-create",
		DeploymentID: pgtype.Text{String: "dep-2", Valid: true},
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("MarkUploadSessionCommitted on committed err = %v; want ErrConflict", err)
	}

	// 10. MarkUploadSessionCommitted on missing row → ErrNotFound.
	if _, err := m.MarkUploadSessionCommitted(ctx, sqlc.MarkUploadSessionCommittedParams{
		ID:           "missing",
		DeploymentID: pgtype.Text{String: "dep-x", Valid: true},
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkUploadSessionCommitted on missing err = %v; want ErrNotFound", err)
	}

	// 11. CancelUploadSession on a fresh open row → status='cancelled'.
	fresh, _ := m.CreateUploadSession(ctx, sqlc.CreateUploadSessionParams{
		ID:        "sess-cancel",
		AccountID: acct,
		AppSlug:   "myapp",
		TotalSize: 512,
		ChunkSize: 4096,
		PartPath:  "/tmp/sess-cancel.part",
	})
	if err := m.CancelUploadSession(ctx, sqlc.CancelUploadSessionParams{
		ID:      "sess-cancel",
		Column2: acct,
	}); err != nil {
		t.Fatalf("CancelUploadSession: %v", err)
	}
	row, _ = m.GetUploadSession(ctx, "sess-cancel")
	if row.Status != "cancelled" {
		t.Errorf("Status after cancel = %q; want cancelled", row.Status)
	}
	_ = fresh

	// 12. CancelUploadSession on terminal row → ErrConflict.
	if err := m.CancelUploadSession(ctx, sqlc.CancelUploadSessionParams{
		ID:      "sess-cancel",
		Column2: acct,
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("CancelUploadSession on terminal err = %v; want ErrConflict", err)
	}

	// 13. CancelUploadSession on missing row → ErrNotFound.
	if err := m.CancelUploadSession(ctx, sqlc.CancelUploadSessionParams{
		ID:      "missing",
		Column2: acct,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("CancelUploadSession on missing err = %v; want ErrNotFound", err)
	}

	// 14. CancelUploadSession with wrong account → ErrConflict (defense-in-depth).
	otherAcct, _ := m.CreateUploadSession(ctx, sqlc.CreateUploadSessionParams{
		ID:        "sess-other-acct",
		AccountID: accountUUIDFromString(t, "22222222-2222-2222-2222-222222222222"),
		AppSlug:   "myapp",
		TotalSize: 512,
		ChunkSize: 4096,
		PartPath:  "/tmp/sess-other-acct.part",
	})
	if err := m.CancelUploadSession(ctx, sqlc.CancelUploadSessionParams{
		ID:      "sess-other-acct",
		Column2: acct,
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("CancelUploadSession wrong account err = %v; want ErrConflict", err)
	}
	_ = otherAcct
}

// TestMemStoreUploadSession_Reaper exercises ReapExpiredUploadSessions,
// ReapStaleUploadPartFiles, and ExpireUploadSession (including the
// idempotent "already terminal" path that production reapers hit
// when a sibling reaper flips status first).
func TestMemStoreUploadSession_Reaper(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := accountUUIDFromString(t, "11111111-1111-1111-1111-111111111111")

	// Row 1: open + already-expired → reaper sweeps.
	seedUploadSession(t, m, struct {
		id          string
		account     string
		slug        string
		totalSize   int64
		received    int64
		status      string
		expiresIn   time.Duration
		lastPatched time.Duration
	}{
		id: "expired-open", account: "11111111-1111-1111-1111-111111111111",
		slug: "a", totalSize: 100, received: 0, status: "open",
		expiresIn: -1 * time.Hour, lastPatched: 0,
	})

	// Row 2: open + future expiry → reaper skips.
	seedUploadSession(t, m, struct {
		id          string
		account     string
		slug        string
		totalSize   int64
		received    int64
		status      string
		expiresIn   time.Duration
		lastPatched time.Duration
	}{
		id: "fresh-open", account: "11111111-1111-1111-1111-111111111111",
		slug: "a", totalSize: 100, received: 0, status: "open",
		expiresIn: 24 * time.Hour, lastPatched: 0,
	})

	// Row 3: committed + last_patched 2h ago → stale sweep picks up.
	seedUploadSession(t, m, struct {
		id          string
		account     string
		slug        string
		totalSize   int64
		received    int64
		status      string
		expiresIn   time.Duration
		lastPatched time.Duration
	}{
		id: "stale-committed", account: "11111111-1111-1111-1111-111111111111",
		slug: "a", totalSize: 100, received: 100, status: "committed",
		expiresIn: -1 * time.Hour, lastPatched: -2 * time.Hour,
	})

	// Row 4: committed + last_patched 30m ago → stale sweep skips.
	seedUploadSession(t, m, struct {
		id          string
		account     string
		slug        string
		totalSize   int64
		received    int64
		status      string
		expiresIn   time.Duration
		lastPatched time.Duration
	}{
		id: "recent-committed", account: "11111111-1111-1111-1111-111111111111",
		slug: "a", totalSize: 100, received: 100, status: "committed",
		expiresIn: -1 * time.Hour, lastPatched: -30 * time.Minute,
	})

	// ReapExpiredUploadSessions → only expired-open.
	got, err := m.ReapExpiredUploadSessions(ctx)
	if err != nil {
		t.Fatalf("ReapExpiredUploadSessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "expired-open" {
		t.Errorf("ReapExpiredUploadSessions ids = %v; want [expired-open]", uploadIDs(got))
	}

	// ReapStaleUploadPartFiles → only stale-committed.
	stale, err := m.ReapStaleUploadPartFiles(ctx)
	if err != nil {
		t.Fatalf("ReapStaleUploadPartFiles: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != "stale-committed" {
		t.Errorf("ReapStaleUploadPartFiles ids = %v; want [stale-committed]", uploadIDsStale(stale))
	}

	// ExpireUploadSession happy path.
	if err := m.ExpireUploadSession(ctx, "expired-open"); err != nil {
		t.Fatalf("ExpireUploadSession: %v", err)
	}
	if row, _ := m.GetUploadSession(ctx, "expired-open"); row.Status != "expired" {
		t.Errorf("Status after expire = %q; want expired", row.Status)
	}

	// ExpireUploadSession idempotent on terminal row → nil, no-op.
	if err := m.ExpireUploadSession(ctx, "stale-committed"); err != nil {
		t.Errorf("ExpireUploadSession on committed err = %v; want nil", err)
	}

	// ExpireUploadSession on missing row → ErrNotFound.
	if err := m.ExpireUploadSession(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ExpireUploadSession(missing) err = %v; want ErrNotFound", err)
	}
	_ = acct
}

// TestMemStoreUploadSession_CommitDedupe exercises
// RecordUploadCommitOutcome + GetUploadCommitOutcome — the companion
// table that gives POST /v1/uploads/{id}/commit its retry idempotency.
func TestMemStoreUploadSession_CommitDedupe(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// First commit → row recorded.
	if _, err := m.RecordUploadCommitOutcome(ctx, sqlc.RecordUploadCommitOutcomeParams{
		UploadID:     "u1",
		DeploymentID: "dep-1",
		BuildID:      "build-1",
	}); err != nil {
		t.Fatalf("RecordUploadCommitOutcome first: %v", err)
	}
	got, err := m.GetUploadCommitOutcome(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUploadCommitOutcome: %v", err)
	}
	if got.DeploymentID != "dep-1" || got.BuildID != "build-1" {
		t.Errorf("dedupe row = %+v; want {dep-1 build-1}", got)
	}

	// Retry of same commit → ErrConflict (handler maps to "use stored outcome").
	if _, err := m.RecordUploadCommitOutcome(ctx, sqlc.RecordUploadCommitOutcomeParams{
		UploadID:     "u1",
		DeploymentID: "dep-1-retry",
		BuildID:      "build-1",
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("RecordUploadCommitOutcome retry err = %v; want ErrConflict", err)
	}

	// Missing dedupe → ErrNotFound.
	if _, err := m.GetUploadCommitOutcome(ctx, "u-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUploadCommitOutcome(missing) err = %v; want ErrNotFound", err)
	}
}

// uploadIDs / uploadIDsStale are tiny helpers for the reaper-test
// error messages so a sweep-drift failure lists all returned IDs at
// once instead of forcing a second pass through -run -v.
func uploadIDs(rows []sqlc.ReapExpiredUploadSessionsRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func uploadIDsStale(rows []sqlc.ReapStaleUploadPartFilesRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}