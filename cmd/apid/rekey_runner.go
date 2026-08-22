// Package main — rekey_runner.go (ADR-089 PR-C).
//
// Runner wraps pkg/rekey.Replayer with two apid-side concerns:
//
//  1. File persistence. The rekey walk is multi-batch (Replayer
//     paginates through app_secrets in (account_id, app_id, key)
//     order). On daemon restart, the next incarnation needs the
//     cursor of the last batch to resume. We persist
//     rekey.RekeyProgress to FAAS_REKEY_PROGRESS_FILE (default
//     /var/lib/faas/rekey-progress.json, mode 0o600) after every
//     batch tick so a crash loses at most cfg.BatchSize rows.
//
//  2. Crash-safe audit actor. pkg/rekey itself doesn't know about
//     audit (it stays a pure walk primitive); the runner threads
//     the "rekey" actor through to the audit log so dashboards
//     filtering on kind='secret.rekeyed' / 'secret.rekey_failed'
//     see a clean separation from user-driven rotates (actor='apid').
//
// Opt-in: apid only constructs a Runner when FAAS_REKEY_ENABLED=true.
// Default behaviour (the flag unset) preserves the v1 no-op posture
// — no file write, no goroutine, no audit noise. Operators who have
// not yet rotated the host identity see exactly zero rekey activity
// in the logs.
//
// Lifecycle: Run blocks until ctx is cancelled (same pattern as
// pkg/eventretention.Cleanup.Run, pkg/grace.Grace.Run). Returns
// ctx.Err() on cancellation; per-row failures are recorded in
// Progress.Failed but do NOT abort the walk.
//
// File format: {"total":N,"rekeyed":N,"skipped":N,"failed":N,"last_id":"<cursor>"}.
// Matches rekey.RekeyProgress exactly so a future operator tool
// (e.g. `gregale rekey status`) can decode without a parallel type.
// The file is JSON, not msgpack/protobuf, because it's tiny and
// human-readable in a pager is a property worth ~50 bytes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/rekey"
)

// rekeyProgressPathDefault is the canonical on-disk location for
// the rekey-progress snapshot. Mirrors /var/lib/faas/audit-hmac.key
// (cmd/apid/main.go:1064) — the same systemd-managed directory.
//
// 0o600 because the file reveals two operational secrets: how many
// customer envelopes the host failed to re-seal (i.e. which kid
// migrations are stuck), and the last-seen cursor (which combines
// account_id + app_id + key — knowing the key exists leaks customer
// configuration shape). Both are observability-grade PII; we don't
// want a sibling user reading the file.
const rekeyProgressPathDefault = "/var/lib/faas/rekey-progress.json"

// rekeyProgressEnvVar overrides rekeyProgressPathDefault. The
// override exists for the e2e harness (each test gets its own
// tmpdir, no risk of stale progress carrying across runs) and for
// operators with non-standard state directories.
const rekeyProgressEnvVar = "FAAS_REKEY_PROGRESS_FILE"

// rekeyEnabledEnvVar is the opt-in toggle. The constant lives here
// (next to the file-path env var) so an operator scanning the source
// for `FAAS_REKEY_*` finds both in one place — same convention as
// auditHMACKeyEnvVar (cmd/apid/main.go:1153) and sourceSpoolRootEnv
// (cmd/apid/deploy_inputs.go:34).
const rekeyEnabledEnvVar = "FAAS_REKEY_ENABLED"

// rekeyTruthyLiterals are the case-insensitive truthy spellings the
// runner accepts for FAAS_REKEY_ENABLED. Name-spaced (NOT a global
// `truthyFlagLiterals`) because goconst matches literal text across
// the whole package, and reusing deploy_inputs.go's literal would
// tie two unrelated subsystems together — same rationale as the
// memory note on multi-resource goconst hits. A future "off"/"no"
// value addition is one line here.
var rekeyTruthyLiterals = []string{"1", "true", "yes", "on"}

// Runner is the apid-side owner of the rekey background goroutine.
// Constructed once at boot when FAAS_REKEY_ENABLED=true; nil
// otherwise. Held on *server so the HTTP handler
// (handlers_rekey.go) can read the latest progress snapshot.
//
// Public surface is intentionally minimal: Run(ctx) for the
// goroutine, Progress() for the handler, writeProgress(p) for tests
// (the test seam is the persistence path, not the walk itself —
// the walk is exercised end-to-end via the e2e).
type Runner struct {
	store    rekey.Store // narrow interface, not state.Store (see pkg/rekey)
	audit    *audit.Auditor
	replayer *rekey.Replayer
	progPath string // absolute path; empty = memory-only
	log      *slog.Logger
	lastProg atomic.Pointer[rekey.RekeyProgress]
}

// RunnerOpts is the NewRunner parameter struct. Kept separate
// from Runner so test code can construct without importing
// age (the identities slice is the only age-typed field).
type RunnerOpts struct {
	// ProgressPath is the file the runner writes the cumulative
	// progress snapshot to after every batch tick. Empty string
	// disables file persistence — the runner runs memory-only,
	// returning the latest snapshot via Progress() but losing it
	// on restart. Used in tests where /var/lib/faas is read-only.
	ProgressPath string
	// Identities is the OpenMulti slice (current + previous)
	// loaded from /etc/faas/secrets/host.age{,.previous}. Index
	// 0 is the current identity; the rekey seals every row under
	// identities[0] and unseals any of the loaded identities.
	Identities []*age.X25519Identity
	// Config is the Replayer pacing config. Zero value triggers
	// the production defaults (100 rows/sec, 50/batch, 5s/row
	// unseal timeout).
	Config rekey.RekeyConfig
	// Audit is the apid-side audit handle. Emitted with actor="rekey"
	// so dashboards can filter background re-seals distinctly from
	// user-driven rotates (actor="apid", PR-B).
	Audit *audit.Auditor
	// Store is the platform-wide state.Store, narrowed to the
	// rekey.Store interface inside pkg/rekey. Both PgStore and
	// MemStore satisfy it (see pkg/state/pgstore.go, memstore.go).
	Store rekey.Store
	// HostHMACKey is the per-host 32-byte key that the rekey
	// worker uses to compute value_hash for every re-Seal row
	// (ADR-117 PR-C). The key is loaded once at apid startup
	// from /etc/faas/secrets/host.hmac.key (cmd/apid/main.go::
	// loadHostHMACKey) and threaded in via the bgBefore
	// closure. Required (not optional) — the rekey pass stamps
	// value_hash on every re-Seal row, and an empty key would
	// silently degrade the env-diff discriminator to a
	// non-discriminator. rekey.New() refuses construction on
	// empty.
	HostHMACKey []byte
	// Log is the structured logger. Required (not optional) so a
	// misconfigured daemon doesn't silently drop walk failures.
	Log *slog.Logger
}

// NewRunner constructs a Runner. Returns an error if opts is
// incomplete (nil store, empty identities, etc.) — a typo'd env
// var shouldn't silently degrade to "no rekey, no log".
//
// The runner does NOT start the goroutine — the caller is
// responsible for `go func() { _ = runner.Run(ctx) }()`. This
// keeps the lifecycle symmetrical with pkg/grace.Grace and
// pkg/eventretention.Cleanup (both constructed + launched from
// cmd/apid/main.go's bgBefore closure).
func NewRunner(opts RunnerOpts) (*Runner, error) {
	if opts.Log == nil {
		return nil, errors.New("rekey runner: nil logger")
	}
	if opts.Store == nil {
		return nil, errors.New("rekey runner: nil store")
	}
	if opts.Audit == nil {
		return nil, errors.New("rekey runner: nil audit")
	}
	if len(opts.Identities) == 0 || opts.Identities[0] == nil {
		return nil, errors.New("rekey runner: empty identities")
	}
	rp, err := rekey.New(opts.Store, opts.Identities, opts.HostHMACKey, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("rekey runner: build replayer: %w", err)
	}
	r := &Runner{
		store:    opts.Store,
		audit:    opts.Audit,
		replayer: rp,
		progPath: opts.ProgressPath,
		log:      opts.Log,
	}
	// Best-effort: hydrate lastProg from a previous on-disk
	// snapshot. A missing file is not an error (first boot on a
	// fresh node); a corrupt file logs Warn and starts from zero
	// (operator-visible degraded mode, not a panic).
	r.loadProgressFromDisk()
	return r, nil
}

// Run walks app_secrets, re-sealing every row under the current
// host identity. Blocks until ctx is cancelled; returns ctx.Err()
// on cancellation. Per-row failures are recorded in Progress.Failed
// but do not abort the walk.
//
// The progress callback fires at the end of each batch (inside
// pkg/rekey.Run) and updates r.lastProg + persists to disk. The
// HTTP handler reads r.lastProg via Progress(); readers never
// block on the walk.
//
// The walk is single-shot — Run consumes the supplied context
// once. On daemon restart, NewRunner hydrates the cursor from
// disk and the next Run resumes from there.
func (r *Runner) Run(ctx context.Context) error {
	r.log.Info("rekey: walk starting",
		"progress_path", r.progPath,
		"cursor", r.currentCursor(),
	)
	err := r.replayer.Run(ctx, r.currentCursor(), func(p rekey.RekeyProgress) {
		// writeProgress updates lastProg (in-process snapshot for
		// the HTTP handler) and, when a file path is configured,
		// atomically renames a fresh JSON file. See the comment
		// on writeProgress for the failure-mode contract — the
		// returned error is intentionally swallowed (logged inside
		// writeProgress itself) because crashing the walk over a
		// disk hiccup would lose progress; the next batch tick
		// re-attempts the rename with a fresh snapshot.
		_ = r.writeProgress(p)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		r.log.Warn("rekey: walk returned", "err", err)
		return err
	}
	return nil
}

// Progress returns the most recent rekey snapshot. Safe for
// concurrent readers — backed by atomic.Pointer. Returns the zero
// value if no batch has completed yet.
func (r *Runner) Progress() rekey.RekeyProgress {
	if p := r.lastProg.Load(); p != nil {
		return *p
	}
	return rekey.RekeyProgress{}
}

// currentCursor returns the LastID from the loaded snapshot, or
// the empty string (start-from-zero) if nothing has loaded.
func (r *Runner) currentCursor() string {
	if p := r.lastProg.Load(); p != nil {
		return p.LastID
	}
	return ""
}

// writeProgress updates the in-process lastProg snapshot AND, when
// the runner has a file path, atomically replaces the on-disk
// snapshot. The on-disk write mirrors
// pkg/secretbox.WriteHostKeyAtPath (tmp + chmod + rename) so a
// crash mid-write leaves either the old snapshot or the new one
// — never a half-written file.
//
// The in-process update happens unconditionally — even in
// memory-only mode (progPath empty) the HTTP handler must see
// the latest tick. The on-disk update is best-effort: a Warn log
// on failure, never propagated. Crashing the daemon over a disk
// hiccup would abort the walk; losing a progress snapshot is
// recoverable (the next batch re-writes it).
//
// If the parent directory is missing (test-as-non-root), the
// first call logs Warn and disables the writer — same fallback
// shape as the audit-hmac loader (cmd/apid/main.go:1131-1152).
func (r *Runner) writeProgress(p rekey.RekeyProgress) error {
	// In-process snapshot first — handlers reading Progress()
	// must see the latest tick even when disk persistence is
	// disabled.
	r.lastProg.Store(&p)
	if r.progPath == "" {
		return nil
	}
	dir := filepath.Dir(r.progPath)
	if _, statErr := os.Stat(dir); statErr != nil {
		r.log.Warn("rekey: progress file dir unavailable; running memory-only",
			"path", r.progPath, "err", statErr)
		r.progPath = "" // disable further attempts
		return nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		// json.Marshal of the struct-of-ints can only fail if
		// someone adds an unsupported field (chan, func); not
		// reachable today. Log + swallow — see comment above.
		r.log.Warn("rekey: marshal progress", "err", err)
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".rekey-progress.tmp.*")
	if err != nil {
		r.log.Warn("rekey: create tmp", "path", r.progPath, "err", err)
		return nil
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if the rename never landed. The
		// close inside the success path will have already
		// happened; the unlink is the safety net for the
		// error-path branches.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		r.log.Warn("rekey: write tmp", "path", tmpName, "err", err)
		return nil
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		r.log.Warn("rekey: chmod tmp", "path", tmpName, "err", err)
		return nil
	}
	if err := tmp.Close(); err != nil {
		r.log.Warn("rekey: close tmp", "path", tmpName, "err", err)
		return nil
	}
	if err := os.Rename(tmpName, r.progPath); err != nil {
		r.log.Warn("rekey: rename into place", "path", r.progPath, "err", err)
		return nil
	}
	return nil
}

// loadProgressFromDisk hydrates lastProg from the on-disk snapshot.
// Missing file = no-op (first boot). Corrupt file = log Warn + start
// from zero (don't fail boot over a stale file — the walk will
// re-traverse already-done rows; the seen-set inside Replayer dedupes).
func (r *Runner) loadProgressFromDisk() {
	if r.progPath == "" {
		return
	}
	data, err := os.ReadFile(r.progPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.log.Warn("rekey: read progress file", "path", r.progPath, "err", err)
		}
		return
	}
	var p rekey.RekeyProgress
	if err := json.Unmarshal(data, &p); err != nil {
		r.log.Warn("rekey: parse progress file; starting from zero",
			"path", r.progPath, "err", err)
		return
	}
	r.lastProg.Store(&p)
	r.log.Info("rekey: resumed from on-disk progress",
		"path", r.progPath,
		"total", p.Total,
		"rekeyed", p.Rekeyed,
		"failed", p.Failed,
		"last_id", p.LastID,
	)
}
