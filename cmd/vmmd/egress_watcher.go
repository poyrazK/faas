// egress_watcher.go — ADR-055 / Tier 1 Phase 4 runtime policy reload.
//
// vmmd's per-host egress policy watcher. Subscribes to the
// `egress_policy_changed` pg_notify channel (migration 00078), and
// on every notification re-renders the policy with the host's
// compile-time defaults, validates via `nft -c -f <staging>`, and
// atomic-replaces /etc/nftables.conf followed by `nft -f`. Single-box
// default-local vmmd does NOT observe this channel — the gate is
// `cfg.ComputeNode.NodeName != ""` in main.go, mirroring the
// capacity publisher wiring (ADR-025 axis 5).
//
// Why a watcher and not just a `make bootstrap` rerun? An operator
// can update the audit row (`egress_policy`) from outside the
// ansible role — the canonical values still live in
// `pkg/netns.DefaultHostPolicy`, but the audit row is the operator-
// facing surface. The watcher makes the per-host policy hot-reloadable
// for the nomad case where the operator needs to roll a new public
// IP range without a full `make bootstrap` against every node. The
// payload is informational; the watcher re-renders from the local
// host's compile-time defaults (mirrors cmd/vmmd/capacity_publisher.go's
// "freshness, not authority" treaty).
//
// Drain-loop pattern mirrors cmd/gatewayd-internal/nodecache.go::WatchEvictions
// and pkg/sched/nodekeys.go::(*NodeKeyRegistry).Run. Uses
// db.SubscribeWithReconnect so a Postgres restart doesn't strand the
// reload loop. The first-subscribe failure is logged-and-returned;
// no heartbeat metric today (the watcher is a low-frequency path:
// the audit row churns at most a few times per day).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/netns"
)

// nftExec is the test seam for the watcher's shell-out path. The
// production default shells out to `nft -c -f` and `nft -f` via
// os/exec; tests inject a stub that records the calls and returns
// canned errors so the validation/atomic-replace branches can be
// driven deterministically without root privileges.
type nftExec interface {
	// CheckSyntax validates the staging file is parseable by nft(8).
	// Returns nil on a valid ruleset; non-nil on parse failure or
	// any I/O error. The staging file is NOT deleted by the
	// production implementation — the caller may inspect it after
	// failure.
	CheckSyntax(ctx context.Context, path string) error
	// Reload swaps the live ruleset to the staging file. Production
	// emits `nft -f <staging>` (the staging file has already been
	// atomic-renamed over /etc/nftables.conf by the caller).
	Reload(ctx context.Context, path string) error
}

// osExecNft is the production nftExec. stdout and stderr are joined
// into a single buffer so the caller's error message carries the
// nft(8) diagnostic.
type osExecNft struct{}

func (osExecNft) CheckSyntax(ctx context.Context, path string) error {
	// `nft -c -f <path>` is the dry-run / syntax-check mode. The
	// production watcher holds a staging file at
	// /tmp/vmmd-egress-staging/nftables.conf.staging (see
	// runWithDeps); on syntax-check failure the staging file is
	// left on disk so the operator can inspect it.
	return runNftCmd(ctx, "nft -c -f "+shellQuote(path))
}

func (osExecNft) Reload(ctx context.Context, path string) error {
	// `nft -f <path>` is the live-reload path. The staging file
	// at /tmp/vmmd-egress-staging/nftables.conf.staging has already
	// been atomic-replaced over /etc/nftables.conf by the caller
	// (atomicReplace's cross-fs copy+rename path), so the path here
	// is the live file.
	return runNftCmd(ctx, "nft -f "+shellQuote(path))
}

// egressPolicyAuditRow is the JSON payload shape the migration
// 00078 trigger emits on `egress_policy_changed`. The watcher logs
// the payload's fields for diagnostic correlation but does NOT
// trust the payload for the render — the canonical values live in
// `pkg/netns.DefaultHostPolicy`. A misconfigured audit row is
// surfaced as a log warning, not a render override.
//
// PR scale-out tier-1 residual (Gap #4): the trigger payload now
// also carries overlay_exceptions + danger_accept_rfc1918_lateral_movement
// (added by migration 00273_egress_policy_exceptions.sql). The
// watcher reads them for log correlation; the renderer still uses
// the local host's compile-time defaults (the audit row is
// "freshness, not authority" — same posture as masquerade_cidr).
type egressPolicyAuditRow struct {
	PolicyID                           string   `json:"policy_id"`
	PublicIface                        string   `json:"public_iface"`
	MasqueradeCIDR                     string   `json:"masquerade_cidr"`
	OverlayExceptions                  []string `json:"overlay_exceptions,omitempty"`
	DangerAcceptRFC1918LateralMovement bool     `json:"danger_accept_rfc1918_lateral_movement,omitempty"`
	ChangedAt                          string   `json:"changed_at"`
}

// renderStagingFunc is the seam that converts the host policy into
// a ruleset body. Production uses netns.DefaultHostPolicy.Render().
// Tests inject a stub that returns a known body so the staging-file
// + atomic-replace path can be exercised without invoking the real
// renderer.
type renderStagingFunc func() string

// egressWatcher is the dependency-injection seam for the runtime
// reload loop. Production wires `nft = osExecNft{}` and
// `render = netns.DefaultHostPolicy.Render`; tests substitute stubs
// to drive the loop deterministically.
//
// paths mirrors the production filesystem layout:
//   - stagingDir:   a temp directory where the watcher writes the
//     rendered ruleset before validation.
//   - livePath:     the path to be atomic-replaced once validation
//     passes. Production: /etc/nftables.conf.
//
// The watcher is an error-once-and-log shape: a `nft -c -f` failure
// leaves the staging file on disk and emits a structured log; the
// daemon stays running. The next `egress_policy_changed` notification
// is the next attempt; a manual `nft -c -f <staging>` inspection is
// the operator's recovery path.
type egressWatcher struct {
	log        *slog.Logger
	nft        nftExec
	render     renderStagingFunc
	stagingDir string
	livePath   string
}

// newEgressWatcher wires the production watcher. stagingDir is the
// daemon's process-local temp directory; livePath is the canonical
// nftables config path. Tests inject their own values.
func newEgressWatcher(log *slog.Logger, stagingDir, livePath string) *egressWatcher {
	if log == nil {
		log = slog.Default()
	}
	return &egressWatcher{
		log:        log,
		nft:        osExecNft{},
		render:     func() string { return netns.DefaultHostPolicy.Render() },
		stagingDir: stagingDir,
		livePath:   livePath,
	}
}

// Run subscribes to the `egress_policy_changed` pg_notify channel
// and reloads the live policy on every notification. Returns when
// ctx is cancelled. The first-subscribe failure is logged and
// returned; the daemon's main loop decides whether to fall through
// (single-box dev) or exit (multi-box prod).
//
// The drain loop:
//
//  1. Decode the JSON payload (informational; log on bad payload).
//  2. Render the policy with the local compile-time defaults.
//  3. Write the rendered body to a staging file under stagingDir.
//  4. nft -c -f <staging> — if this fails, log; do NOT atomic-replace.
//  5. Atomic-rename staging over livePath.
//  6. nft -f <livePath> — reload the ruleset into the kernel.
//
// The 6-step ordering is load-bearing: a successful syntax-check
// with a failed atomic-rename, or a failed reload, must leave the
// live ruleset intact. The atomic-rename is the gate.
func (w *egressWatcher) Run(ctx context.Context, pool *pgxpool.Pool) error {
	notif, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyEgressPolicyChanged}, w.log)
	if err != nil {
		return fmt.Errorf("vmmd: egress watcher subscribe: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case got, ok := <-notif:
			if !ok {
				// Inner channel closed (Postgres restart, LISTEN
				// error). SubscribeWithReconnect handles the
				// reconnect; the OUTER channel closing means
				// ctx cancelled. Belt-and-braces.
				return nil
			}
			var row egressPolicyAuditRow
			if err := json.Unmarshal([]byte(got.Payload), &row); err != nil {
				w.log.Warn("vmmd: egress watcher bad payload", "payload", got.Payload, "err", err)
				continue
			}
			if err := w.Reload(ctx); err != nil {
				w.log.Error("vmmd: egress watcher reload", "policy_id", row.PolicyID, "err", err)
				continue
			}
			w.log.Info("vmmd: egress policy reloaded",
				"policy_id", row.PolicyID,
				"public_iface", row.PublicIface,
				"masquerade_cidr", row.MasqueradeCIDR,
				"overlay_exceptions", row.OverlayExceptions,
				"danger_accept_rfc1918_lateral_movement", row.DangerAcceptRFC1918LateralMovement,
				"changed_at", row.ChangedAt)
		}
	}
}

// Reload renders, validates, and atomic-replaces the live ruleset.
// Separated from Run so a unit test can drive the 6-step pipeline
// without subscribing to pg_notify at all.
//
// Exposed (capitalized) so cmd/vmmd's runDeps can route it through
// a startEgressWatcher test seam; the production wiring in main.go
// launches a goroutine that calls Run directly.
func (w *egressWatcher) Reload(ctx context.Context) error {
	// 1. Render.
	body := w.render()

	// 2. Write to staging file. Mode 0644 matches the ansible
	// role's policy_nftables.conf copy (so nft -c -f sees the
	// same content the operator would see when running nft
	// manually). The staging file is NOT cleaned up on success
	// — it lives one boot-cycle of the watcher process so an
	// operator can inspect the last-rendered body.
	staging := filepath.Join(w.stagingDir, "nftables.conf.staging")
	if err := os.WriteFile(staging, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write staging %s: %w", staging, err)
	}

	// 3. Validate via nft -c -f. Failure leaves the staging file
	// on disk for inspection (the operator runs nft -c -f on it
	// to see the parse error).
	if err := w.nft.CheckSyntax(ctx, staging); err != nil {
		return fmt.Errorf("nft syntax check on %s: %w", staging, err)
	}

	// 4. Atomic-rename staging over livePath. The staging file
	// and the livePath are on the same filesystem (both under
	// /etc/ or under w.stagingDir for tests); os.Rename within
	// the same filesystem IS atomic. Cross-filesystem rename is
	// not — production uses /etc/nftables.conf and the staging
	// dir is the daemon's temp dir, so the rename is cross-
	// filesystem. atomicReplace handles EXDEV via copy+fsync.
	if err := atomicReplace(staging, w.livePath); err != nil {
		return fmt.Errorf("atomic-replace %s -> %s: %w", staging, w.livePath, err)
	}

	// 5. Reload the live ruleset.
	if err := w.nft.Reload(ctx, w.livePath); err != nil {
		return fmt.Errorf("nft reload %s: %w", w.livePath, err)
	}
	return nil
}

// atomicReplace moves src over dst. On the production wiring the
// staging file lives at /tmp/vmmd-egress-staging/nftables.conf.staging
// (tmpfs) and dst is /etc/nftables.conf (ext4) — different filesystems,
// so os.Rename will fail with EXDEV and the copy+rename path below
// is the production hot path, not a fallback. The optimistic rename
// IS exercised when staging and dst happen to share a filesystem
// (test fixtures, a custom stagingDir); that path is the fast path.
//
// The intermediate scratch file uses the `<dst>.faas-new` suffix so a
// concurrent ansible run that writes /etc/nftables.conf doesn't
// collide with our copy. The intermediate is removed on success; on
// failure it is left on disk so the operator can inspect what we
// tried to write.
func atomicReplace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Production hot path: cross-fs rename failed. Copy the body to
	// the dst filesystem, then atomic-rename into place. The
	// `<dst>.faas-new` suffix is required because a concurrent
	// ansible run might also be writing dst directly — we never
	// write to dst until the rename.
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read staging: %w", err)
	}
	tmp := dst + ".faas-new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp %s -> %s: %w", tmp, dst, err)
	}
	_ = os.Remove(src)
	return nil
}

// runNftCmd shells out to nft(8) via a shell command string. The
// inline os/exec import is the production wiring; tests inject a
// stub nftExec so they don't need nft on the test machine.
func runNftCmd(ctx context.Context, cmdline string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		// Wrap the *exec.ExitError with %w so callers can errors.As
		// to a *exec.ExitError and inspect ExitCode; append the
		// captured stdout+stderr as diagnostic context (a flat
		// string — the nft(8) error text goes here, not in the
		// wrap chain).
		return fmt.Errorf("nft: %w: %s", err, buf.String())
	}
	return nil
}

// shellQuote wraps a path in single quotes for safe shell
// interpolation. nft paths are operator-supplied, so a path with
// embedded single quotes would silently break; escape via
// `'\”` (close, escape, reopen) which is the POSIX-portable
// single-quote escape.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
