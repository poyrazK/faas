// rekey_test.go — unit tests for pkg/rekey.
//
// Tests use a tiny in-memory fakeStore (the methods pkg/rekey
// uses — ListAppSecretsForRekey, ResealAppSecretWithKidAndValueHashInScope). The
// fakeStore matches the cursor encoding pgstore + memstore use so
// the tests exercise the same wire shape.
//
// Coverage:
//   - Run on an empty store is a no-op (zero rows visited).
//   - Run with rows under previous kid re-seals and stamps kid.
//   - Idempotent: Run twice is a no-op the second time.
//   - Cursor walk: a non-empty cursor skips rows <= cursor.
//   - Constructor: empty identities slice rejected.
//   - Constructor: zero/negative cfg fields fall back to defaults.
package rekey

import (
	"context"
	"crypto/rand"
	"sort"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// newTestHMACKey returns a fresh 32-byte random HMAC key for
// the rekey run to stamp value_hash with (ADR-117 PR-C). The
// key is per-test; the Replayer copies it internally.
func newTestHMACKey(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read for test HMAC key: %v", err)
	}
	return b
}

// fakeStore implements just the two methods pkg/rekey uses.
// Cursor encoding matches pgstore/pgstore_memstore
// ("<account_id>|<app_id>|<scope>|<key>"). ADR-092 PR-A widened
// the encoding from 3-segment to 4-segment; this test fake
// mirrors that and accepts 4-segment cursors split into
// (AccountID, AppID, Scope, Key). In-flight 3-segment cursors
// from pre-PR A Replayer instances are tolerated for the crash
// recovery path: splitCursor lazy-collapses them with scope =
// "default".
type fakeStore struct {
	mu   sync.Mutex
	rows map[string]state.AppSecret
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]state.AppSecret{}}
}

func (s *fakeStore) put(row state.AppSecret) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row.Scope == "" {
		row.Scope = state.DefaultEnvScope
	}
	s.rows[encodeCursor(row.AccountID, row.AppID, row.Scope, row.Key)] = row
}

func (s *fakeStore) ListAppSecretsForRekey(_ context.Context, limit int, cursor string) ([]state.AppSecret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var curA, curB, curC, curD string
	if cursor != "" {
		parts := splitCursor(cursor)
		if parts == nil {
			return nil, nil
		}
		curA, curB, curC, curD = parts[0], parts[1], parts[2], parts[3]
	}
	var out []state.AppSecret
	for _, r := range s.rows {
		// Crash-safety: pgstore uses COMPOSITE >= so a row
		// whose cursor matches the current row is included
		// (matching cursor returns the row; seen-set inside
		// Replayer dedupes). The lessOrEqQuads helper
		// encodes the strict-less filter; >= semantics map
		// to "less than the cursor" being excluded.
		if curA != "" && lessQuads(r.AccountID, r.AppID, r.Scope, r.Key, curA, curB, curC, curD) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessQuads(out[i].AccountID, out[i].AppID, out[i].Scope, out[i].Key,
			out[j].AccountID, out[j].AppID, out[j].Scope, out[j].Key)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeStore) ResealAppSecretWithKidAndValueHashInScope(_ context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[encodeCursor(accountID, appID, scope, key)]
	row.AccountID = accountID
	row.AppID = appID
	row.Scope = scope
	row.Key = key
	row.Kid = kid
	row.ValueHash = valueHash
	row.Ciphertext = ciphertext
	s.rows[encodeCursor(accountID, appID, state.DefaultEnvScope, key)] = row
	return nil
}

// splitCursor splits a cursor string on '|'. Returns 4-tuple parts
// for the canonical post-PR-A encoding; lazy 3→4 fallback treats
// a 3-segment cursor as (acct, app, "default", key) to match the
// pgstore/memstore fall-back semantics.
func splitCursor(c string) []string {
	out := []string{""}
	for i := 0; i < len(c); i++ {
		if c[i] == '|' {
			out = append(out, "")
			continue
		}
		out[len(out)-1] += string(c[i])
	}
	switch len(out) {
	case 4:
		return out
	case 3:
		// In-flight 3-segment cursor from a pre-PR-A Replayer
		// resumed against a post-PR-A store. Insert the
		// canonical scope='default' as the 3rd segment.
		return []string{out[0], out[1], state.DefaultEnvScope, out[2]}
	default:
		return nil
	}
}

func lessQuads(a1, a2, a3, a4, b1, b2, b3, b4 string) bool {
	if a1 != b1 {
		return a1 < b1
	}
	if a2 != b2 {
		return a2 < b2
	}
	if a3 != b3 {
		return a3 < b3
	}
	return a4 < b4
}

// sealUnder is a one-shot seal helper using pkg/secretbox.Seal so
// the test exercises the real envelope shape end-to-end.
func sealUnder(t *testing.T, recipient *age.X25519Recipient, key, value string) []byte {
	t.Helper()
	blob, err := secretbox.Seal(recipient, secretbox.Envelope{key: value})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return blob
}

// TestRun_EmptyStore: Run on an empty store is a clean no-op.
func TestRun_EmptyStore(t *testing.T) {
	id := mustIdentity(t)
	store := newFakeStore()
	r, err := New(store, []*age.X25519Identity{id}, newTestHMACKey(t), RekeyConfig{RowsPerSecond: 1000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var last RekeyProgress
	err = r.Run(context.Background(), "", func(p RekeyProgress) { last = p })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if last.Total != 0 || last.Rekeyed != 0 || last.Skipped != 0 || last.Failed != 0 {
		t.Fatalf("empty store: got %+v, want zero counters", last)
	}
}

// TestRun_RekeysRowsUnderPreviousKid: rows sealed under the
// previous identity get re-sealed under current and have kid
// updated to the current identity's fingerprint.
func TestRun_RekeysRowsUnderPreviousKid(t *testing.T) {
	previous := mustIdentity(t)
	current := mustIdentity(t)

	store := newFakeStore()
	// Seed three rows under the previous identity with kid =
	// previous's recipient string.
	prevKid := previous.Recipient().String()
	for i, key := range []string{"A", "B", "C"} {
		store.put(state.AppSecret{
			AccountID:  "acct-1",
			AppID:      "app-1",
			Key:        key,
			Ciphertext: sealUnder(t, previous.Recipient(), key, "v"+string(rune('0'+i))),
			Kid:        prevKid,
		})
	}

	r, err := New(store, []*age.X25519Identity{current, previous}, newTestHMACKey(t), RekeyConfig{RowsPerSecond: 5000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var last RekeyProgress
	if err := r.Run(context.Background(), "", func(p RekeyProgress) { last = p }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if last.Total != 3 || last.Rekeyed != 3 || last.Skipped != 0 || last.Failed != 0 {
		t.Fatalf("got %+v, want Total=3 Rekeyed=3", last)
	}

	// Each row's kid must now equal the current identity's
	// recipient string.
	wantKid := current.Recipient().String()
	for _, key := range []string{"A", "B", "C"} {
		row, ok := store.rows[encodeCursor("acct-1", "app-1", state.DefaultEnvScope, key)]
		if !ok {
			t.Fatalf("row %q missing after rekey", key)
		}
		if row.Kid != wantKid {
			t.Fatalf("row %q kid = %q, want %q", key, row.Kid, wantKid)
		}
	}
}

// TestRun_Idempotent: running Replayer twice is a no-op the
// second time — every row has kid = current after the first pass.
func TestRun_Idempotent(t *testing.T) {
	previous := mustIdentity(t)
	current := mustIdentity(t)

	store := newFakeStore()
	store.put(state.AppSecret{
		AccountID:  "acct-1",
		AppID:      "app-1",
		Key:        "A",
		Ciphertext: sealUnder(t, previous.Recipient(), "A", "v"),
		Kid:        previous.Recipient().String(),
	})

	r, err := New(store, []*age.X25519Identity{current, previous}, newTestHMACKey(t), RekeyConfig{RowsPerSecond: 5000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First pass: rekeys the row.
	var p1 RekeyProgress
	if err := r.Run(context.Background(), "", func(p RekeyProgress) { p1 = p }); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if p1.Rekeyed != 1 {
		t.Fatalf("first pass: got %+v, want Rekeyed=1", p1)
	}

	// Second pass: skips everything (kid == current).
	var p2 RekeyProgress
	if err := r.Run(context.Background(), "", func(p RekeyProgress) { p2 = p }); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if p2.Rekeyed != 0 || p2.Skipped != 1 {
		t.Fatalf("second pass: got %+v, want Rekeyed=0 Skipped=1", p2)
	}
}

// TestRun_CursorResumes: a non-empty cursor starts the walk AT OR
// AFTER the cursor tuple. With the >= fence, the cursor row is
// returned in the next batch but the per-Run seen-set drops it
// as "already visited" — so on resume only strictly-after rows
// are newly processed. Mirrors daemon-restart resumption: the
// new Run starts with an empty seen-set, so its first batch
// picks up exactly the cursor row + everything after, and
// processes rows-after-cursor (the cursor row itself is deduped
// to "we visited this in the previous Run, kid is current").
func TestRun_CursorResumes(t *testing.T) {
	previous := mustIdentity(t)
	current := mustIdentity(t)
	store := newFakeStore()
	for _, key := range []string{"A", "B", "C"} {
		store.put(state.AppSecret{
			AccountID:  "acct-1",
			AppID:      "app-1",
			Key:        key,
			Ciphertext: sealUnder(t, previous.Recipient(), key, "v"),
			Kid:        previous.Recipient().String(),
		})
	}

	r, err := New(store, []*age.X25519Identity{current, previous}, newTestHMACKey(t), RekeyConfig{RowsPerSecond: 5000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Start AT B — the >= fence brings back [B, C] (a row
	// whose cursor matches is included). The seen-set starts
	// empty in this Run, so B and C are both processed for
	// the first time in this Run. Real daemon-restart
	// semantics: the cursor is just a "start from here or
	// later" hint, while the in-Run seen-set prevents the
	// per-row advance + >= fence from looping forever.
	var last RekeyProgress
	if err := r.Run(context.Background(), encodeCursor("acct-1", "app-1", state.DefaultEnvScope, "B"), func(p RekeyProgress) { last = p }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if last.Total != 2 || last.Rekeyed != 2 {
		t.Fatalf("cursor walk: got %+v, want Total=2 Rekeyed=2 (B + C)", last)
	}
}

// TestNew_RejectsEmptyIdentities: constructor precondition contract.
// The HMAC key is passed (ADR-117 PR-C) but is irrelevant — the
// identities check fires first. Also pins the empty-HMAC-key
// rejection.
func TestNew_RejectsEmptyIdentities(t *testing.T) {
	if _, err := New(newFakeStore(), nil, newTestHMACKey(t), RekeyConfig{}); err == nil {
		t.Fatal("expected error for empty identities slice")
	}
	if _, err := New(newFakeStore(), []*age.X25519Identity{nil}, newTestHMACKey(t), RekeyConfig{}); err == nil {
		t.Fatal("expected error for nil current identity")
	}
	if _, err := New(newFakeStore(), []*age.X25519Identity{mustIdentity(t)}, nil, RekeyConfig{}); err == nil {
		t.Fatal("expected error for empty host HMAC key — refusing to run without value_hash key (ADR-117 PR-C)")
	}
}

// TestNew_FillsDefaults: zero cfg fields fall back to
// DefaultRekeyConfig so a unit-test or misconfigured daemon
// doesn't get a runaway goroutine.
func TestNew_FillsDefaults(t *testing.T) {
	id := mustIdentity(t)
	r, err := New(newFakeStore(), []*age.X25519Identity{id}, newTestHMACKey(t), RekeyConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.cfg.RowsPerSecond != DefaultRekeyConfig.RowsPerSecond {
		t.Fatalf("RowsPerSecond = %d, want %d", r.cfg.RowsPerSecond, DefaultRekeyConfig.RowsPerSecond)
	}
	if r.cfg.BatchSize != DefaultRekeyConfig.BatchSize {
		t.Fatalf("BatchSize = %d, want %d", r.cfg.BatchSize, DefaultRekeyConfig.BatchSize)
	}
	if r.cfg.OpenTimeout != DefaultRekeyConfig.OpenTimeout {
		t.Fatalf("OpenTimeout = %v, want %v", r.cfg.OpenTimeout, DefaultRekeyConfig.OpenTimeout)
	}
}

// mustIdentity generates a fresh X25519 identity for tests.
func mustIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	return id
}

// failingFakeStore wraps fakeStore and forces UpsertAppSecretWithKid
// to fail for a specific cursor-encoded row. Used by
// TestRun_CrashMidRow to pin the crash-safety contract:
//
// On a persist failure, the cursor MUST NOT advance past the failed
// row — otherwise the next daemon invocation skips the row that
// still needs re-sealing. After prune-previous on the previous
// identity, the unrekeyed row's envelope becomes permanently
// unreadable.
//
// The fault key is expressed as a full cursor string so callers
// don't need to know the (account_id, app_id, key) tuple shape.
type failingFakeStore struct {
	*fakeStore
	faultCursor string
}

func (s *failingFakeStore) ResealAppSecretWithKidAndValueHashInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error {
	if s.faultCursor != "" && encodeCursor(accountID, appID, scope, key) == s.faultCursor {
		return context.DeadlineExceeded // sentinel "persist failed"
	}
	return s.fakeStore.ResealAppSecretWithKidAndValueHashInScope(ctx, accountID, appID, scope, key, kid, valueHash, ciphertext)
}

// TestRun_CrashMidRow pins the cursor-pin-on-failure + >=
// fence + per-Run seen-set contract. Forces the middle row's
// persist to fail and asserts:
//   - the failed row pins the cursor (LastID == cursor-of-B)
//   - on daemon restart with an empty seen-set, B is
//     re-fetched and re-attempted (Total > 0 on resume)
//   - rows past the failure (C in this fixture) are also
//     visited in the resume Run via the seen-set reset
//   - B's row state (kid = previous) is preserved across the
//     failed persist.
//
// The whole point: a row whose persist fails is NEVER
// silently skipped.
func TestRun_CrashMidRow(t *testing.T) {
	previous := mustIdentity(t)
	current := mustIdentity(t)

	inner := newFakeStore()
	faultRow := encodeCursor("acct-1", "app-1", state.DefaultEnvScope, "B")
	store := &failingFakeStore{fakeStore: inner, faultCursor: faultRow}

	for _, key := range []string{"A", "B", "C"} {
		inner.put(state.AppSecret{
			AccountID:  "acct-1",
			AppID:      "app-1",
			Key:        key,
			Ciphertext: sealUnder(t, previous.Recipient(), key, "v"),
			Kid:        previous.Recipient().String(),
		})
	}

	r, err := New(store, []*age.X25519Identity{current, previous}, newTestHMACKey(t),
		RekeyConfig{RowsPerSecond: 5000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var last RekeyProgress
	if err := r.Run(context.Background(), "", func(p RekeyProgress) { last = p }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Counters: A succeeded (Rekeyed=1), B failed, C succeeded (Rekeyed=2).
	if last.Total != 3 {
		t.Errorf("Total = %d, want 3", last.Total)
	}
	if last.Rekeyed != 2 {
		t.Errorf("Rekeyed = %d, want 2 (A, C)", last.Rekeyed)
	}
	if last.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (B)", last.Failed)
	}
	if last.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", last.Skipped)
	}

	// LastID is PINNED to the first failed row (B), not the
	// last success — so the next Run re-attempts B via >=
	// fence instead of skipping past it.
	wantLast := encodeCursor("acct-1", "app-1", state.DefaultEnvScope, "B")
	if last.LastID != wantLast {
		t.Errorf("LastID = %q, want %q (Pinned to first failure so resume re-attempts B)", last.LastID, wantLast)
	}

	// B's row was NOT updated by the failed persist — still
	// under the previous identity.
	b := inner.rows[encodeCursor("acct-1", "app-1", state.DefaultEnvScope, "B")]
	if b.Kid != previous.Recipient().String() {
		t.Errorf("B's kid = %q, want %q (pre-rekey; persist failed before UPDATE)",
			b.Kid, previous.Recipient().String())
	}

	// Sanity: simulate the daemon restart. New Replayer
	// instance, empty seen-set, cursor=B. The >= fence
	// brings back B + rows-after-B (= C). The fault still
	// fires (the fixture fails B every time), so Failed=1
	// AND C is processed (kid = current → Skipped). This
	// proves B was RE-VISITED, not skipped — the row gets
	// repeatedly attempted until the operator fixes the
	// underlying failure (or runs an escape-hatch UPDATE on
	// the kid column).
	r2, err := New(store, []*age.X25519Identity{current, previous}, newTestHMACKey(t),
		RekeyConfig{RowsPerSecond: 5000, BatchSize: 50})
	if err != nil {
		t.Fatalf("New (resume): %v", err)
	}
	var last2 RekeyProgress
	if err := r2.Run(context.Background(), last.LastID, func(p RekeyProgress) { last2 = p }); err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if last2.Failed != 1 {
		t.Errorf("resume: Failed = %d, want 1 (B re-attempted via >= fence, failed again)", last2.Failed)
	}
	// On the resume, listing returns >= B = [B, C]. seen-set
	// starts empty → both are processed: B fails (Failed=1),
	// C is skipped (Skipped=1, kid already current). Total=2.
	if last2.Total != 2 {
		t.Errorf("resume: Total = %d, want 2 (B re-fetched + C)", last2.Total)
	}
	if last2.Skipped != 1 {
		t.Errorf("resume: Skipped = %d, want 1 (C idempotent — kid=current)", last2.Skipped)
	}
}
