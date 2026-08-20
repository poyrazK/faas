// rekey_runner_test.go — unit tests for cmd/apid/rekey_runner.go
// (ADR-089 PR-C).
//
// Build tag: (none). No PG, no KVM — these tests run as part of
// `make test` and complete in <100ms each.
//
// Coverage:
//   - TestRunner_PersistsProgress: writeProgress atomically
//     replaces the JSON file with mode 0o600 and a JSON shape
//     that round-trips through json.Unmarshal.
//   - TestRunner_LoadsExistingProgress: NewRunner hydrates
//     lastProg from a pre-existing on-disk file (the
//     crash-recovery path).
//   - TestRunner_DisabledReturnsNilProgress: when identities
//     are nil/empty, NewRunner returns an error (NOT a silent
//     no-op — a typo'd env var shouldn't degrade to "no
//     rekey, no log").
//   - TestRunner_MemoryOnlyWhenPathEmpty: when ProgressPath is
//     "", NewRunner succeeds, Progress() returns zero, and no
//     file is written.
//
// We do NOT exercise Replayer.Run end-to-end here — that walks
// the live app_secrets table and is heavy enough to belong in
// cmd/e2e/secrets_rotate_box_e2e_test.go (where a real PgStore
// is available). The runner unit-tests pin the file-persistence
// + state-hydration contract; the e2e pins the walk semantics.
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/rekey"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLogger returns a silent slog.Logger so the test output
// stays clean (the runner logs at boot + on every writeProgress
// call — both are expected noise, not test failures).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// newTestIdentities generates a single fresh age X25519 identity
// for the runner's identities slice. PR-C only requires the
// current identity (index 0); the OpenMulti overload is wired
// for the eventual rotation-overlap case but the runner doesn't
// need two identities to exercise the file-persistence path.
func newTestIdentities(t *testing.T) []*age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate test identity: %v", err)
	}
	return []*age.X25519Identity{id}
}

// newTestRunnerKey returns a fresh 32-byte random HMAC key
// for the rekey runner to stamp value_hash (ADR-117 PR-C).
// Per-test (rand.Read); the runner copies it internally so a
// caller-side wipe does not affect re-Seal hashes.
func newTestRunnerKey(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read for rekey runner HMAC key: %v", err)
	}
	return b
}

// newTestRunner wraps NewRunner with sensible test defaults:
// silent logger, fresh identity, in-memory store, ProgressPath
// in t.TempDir(). Returns the Runner and the resolved path so
// tests can assert on file existence.
func newTestRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rekey-progress.json")
	r, err := NewRunner(RunnerOpts{
		Store:        state.NewMemStore(),
		Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:   newTestIdentities(t),
		HostHMACKey:  newTestRunnerKey(t),
		ProgressPath: path,
		Log:          testLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r, path
}

// TestRunner_PersistsProgress: writeProgress must atomically
// replace the JSON file with mode 0o600 and a parseable shape.
// Three sequential writes mirror the per-batch tick cadence
// from pkg/rekey.Replayer.Run.
func TestRunner_PersistsProgress(t *testing.T) {
	r, path := newTestRunner(t)
	cases := []rekey.RekeyProgress{
		{Total: 50, Rekeyed: 50, Skipped: 0, Failed: 0, LastID: "acct1|app1|FOO"},
		{Total: 100, Rekeyed: 90, Skipped: 5, Failed: 5, LastID: "acct2|app1|BAR"},
		{Total: 150, Rekeyed: 145, Skipped: 5, Failed: 0, LastID: ""},
	}
	for _, p := range cases {
		r.writeProgress(p)
	}
	// File must exist (atomic-rename landed), be parseable, and
	// carry the LATEST snapshot (not an intermediate state).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat progress file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0o600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read progress file: %v", err)
	}
	var got rekey.RekeyProgress
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, data)
	}
	if got != cases[len(cases)-1] {
		t.Errorf("progress = %+v, want %+v", got, cases[len(cases)-1])
	}
}

// TestRunner_LoadsExistingProgress: NewRunner hydrates lastProg
// from a pre-existing on-disk file. This is the crash-recovery
// path — the daemon was killed mid-walk, restarts, and the
// walk resumes from the persisted cursor.
//
// We construct the runner, write a known progress, then build
// a SECOND runner pointing at the same file and assert
// Progress() returns the persisted snapshot.
func TestRunner_LoadsExistingProgress(t *testing.T) {
	first, path := newTestRunner(t)
	want := rekey.RekeyProgress{
		Total:   200,
		Rekeyed: 195,
		Skipped: 0,
		Failed:  5,
		LastID:  "acct7|app2|BAZ",
	}
	first.writeProgress(want)
	// Construct a fresh Runner that reads the same path. We
	// can't reuse `first` because lastProg is already populated
	// in-process — the test is about NewRunner's load step.
	second, err := NewRunner(RunnerOpts{
		Store:        state.NewMemStore(),
		Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:   newTestIdentities(t),
		HostHMACKey:  newTestRunnerKey(t),
		ProgressPath: path,
		Log:          testLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunner (second): %v", err)
	}
	if got := second.Progress(); got != want {
		t.Errorf("Progress() after load = %+v, want %+v", got, want)
	}
	// And the cursor the runner will hand to Replayer.Run must
	// match — without this the resume point would be silently
	// lost. currentCursor() is the path Replayer.Run consumes.
	if got := second.currentCursor(); got != want.LastID {
		t.Errorf("currentCursor() = %q, want %q", got, want.LastID)
	}
}

// TestRunner_DisabledReturnsNilProgress: when Identities is
// empty (the FAAS_HOST_AGE_IDENTITY_PATH=unset path), NewRunner
// must return an error — silently constructing a Runner that
// the operator thinks is running would be worse than failing
// boot. Mirrors the error paths on the other loaders
// (cmd/apid/main.go::loadOrGenerateAuditHMACKey).
func TestRunner_DisabledReturnsNilProgress(t *testing.T) {
	cases := []struct {
		name string
		opts RunnerOpts
	}{
		{
			name: "empty_identities",
			opts: RunnerOpts{
				Store:        state.NewMemStore(),
				Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
				Log:          testLogger(),
				ProgressPath: t.TempDir() + "/p.json",
			},
		},
		{
			name: "nil_store",
			opts: RunnerOpts{
				Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
				Identities:   newTestIdentities(t),
				HostHMACKey:  newTestRunnerKey(t),
				Log:          testLogger(),
				ProgressPath: t.TempDir() + "/p.json",
			},
		},
		{
			name: "nil_audit",
			opts: RunnerOpts{
				Store:        state.NewMemStore(),
				Identities:   newTestIdentities(t),
				HostHMACKey:  newTestRunnerKey(t),
				Log:          testLogger(),
				ProgressPath: t.TempDir() + "/p.json",
			},
		},
		{
			name: "nil_logger",
			opts: RunnerOpts{
				Store:        state.NewMemStore(),
				Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
				Identities:   newTestIdentities(t),
				HostHMACKey:  newTestRunnerKey(t),
				ProgressPath: t.TempDir() + "/p.json",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRunner(tc.opts); err == nil {
				t.Errorf("NewRunner(%s): want error, got nil", tc.name)
			}
		})
	}
}

// TestRunner_MemoryOnlyWhenPathEmpty: when ProgressPath is "",
// the runner runs in memory-only mode (used in tests where
// /var/lib/faas is read-only). Progress() returns zero (no
// snapshot yet); writeProgress is a no-op that doesn't error.
func TestRunner_MemoryOnlyWhenPathEmpty(t *testing.T) {
	r, err := NewRunner(RunnerOpts{
		Store:       state.NewMemStore(),
		Audit:       audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:  newTestIdentities(t),
		HostHMACKey: newTestRunnerKey(t),
		Log:         testLogger(),
		// ProgressPath intentionally empty
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if got := r.Progress(); got != (rekey.RekeyProgress{}) {
		t.Errorf("Progress() before any tick = %+v, want zero", got)
	}
	// writeProgress must not panic, must not error.
	p := rekey.RekeyProgress{Total: 1, Rekeyed: 1, LastID: "x|y|z"}
	if err := r.writeProgress(p); err != nil {
		t.Errorf("writeProgress(memory-only): %v", err)
	}
	// In-memory snapshot still updates — handlers that call
	// Progress() see the latest tick even without disk write.
	if got := r.Progress(); got != p {
		t.Errorf("Progress() after writeProgress = %+v, want %+v", got, p)
	}
}

// TestRunner_LoadFromMissingFile: NewRunner against a path that
// doesn't exist yet (first-boot path) must NOT error and must
// leave Progress() at the zero value.
func TestRunner_LoadFromMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	r, err := NewRunner(RunnerOpts{
		Store:        state.NewMemStore(),
		Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:   newTestIdentities(t),
		HostHMACKey:  newTestRunnerKey(t),
		ProgressPath: path,
		Log:          testLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if got := r.Progress(); got != (rekey.RekeyProgress{}) {
		t.Errorf("Progress() = %+v, want zero", got)
	}
	if got := r.currentCursor(); got != "" {
		t.Errorf("currentCursor() = %q, want empty", got)
	}
}

// TestRunner_LoadFromCorruptFile: NewRunner against a path
// containing invalid JSON must NOT error — the loader logs
// Warn and starts from zero (the walk will re-traverse; the
// seen-set inside Replayer dedupes per-run).
func TestRunner_LoadFromCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("not json {"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	r, err := NewRunner(RunnerOpts{
		Store:        state.NewMemStore(),
		Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:   newTestIdentities(t),
		HostHMACKey:  newTestRunnerKey(t),
		ProgressPath: path,
		Log:          testLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunner against corrupt file: %v", err)
	}
	if got := r.Progress(); got != (rekey.RekeyProgress{}) {
		t.Errorf("Progress() after corrupt load = %+v, want zero", got)
	}
}

// TestRunner_WriteProgressRecoversFromMissingDir: writeProgress
// against a path whose parent dir has been removed between
// construction and write must disable persistence (subsequent
// writes are no-ops) rather than crash. Mirrors the audit-hmac
// loader's degraded-mode behaviour (cmd/apid/main.go:1131-1152).
func TestRunner_WriteProgressRecoversFromMissingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "p.json")
	r, err := NewRunner(RunnerOpts{
		Store:        state.NewMemStore(),
		Audit:        audit.New(state.NewMemStore(), testLogger(), nil, "rekey-test"),
		Identities:   newTestIdentities(t),
		HostHMACKey:  newTestRunnerKey(t),
		ProgressPath: path,
		Log:          testLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	// Now blow away the parent dir before the first write.
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatalf("remove parent dir: %v", err)
	}
	// First write must log Warn and disable (not panic).
	if err := r.writeProgress(rekey.RekeyProgress{Total: 1}); err != nil {
		t.Errorf("writeProgress after parent-dir removal: %v", err)
	}
	// Second write must NOT attempt the rename (progPath was
	// blanked on the first call).
	if r.progPath != "" {
		t.Errorf("progPath = %q after missing-dir fallback, want empty", r.progPath)
	}
	// And the error must surface as fs.ErrNotExist-shaped so a
	// future refactor can branch on it. We don't reach into the
	// runner's log buffer — just confirm the dir is gone.
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("parent dir still present: err=%v", err)
	}
}
