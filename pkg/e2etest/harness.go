// Package e2etest — harness helpers for the M5 acceptance tests in cmd/e2e.
//
// The harness boots every daemon as a real subprocess, each on its own port /
// unix socket inside a t.TempDir(), against one pgtest schema. Tests drive the
// production HTTP / gRPC / pg_notify surface — never the in-process types —
// so the integration is the same code path customers exercise.
//
// Why subprocesses (not in-process wiring):
//
//   - cmd/apid, cmd/schedd, cmd/imaged, cmd/gatewayd-internal, cmd/vmmd, cmd/meterd
//     are all package main; Go forbids importing them as libraries, so the
//     only way to drive the real listener lifecycle is `go build` + `exec.Cmd`.
//   - This matches the EX44 / Lima deployment: every daemon is its own
//     process. If a test passes here, the wire is the same.
//
// Build-tag splits in cmd/e2e:
//
//   - quota_e2e_test.go            (no tag)        boots apid only; CI-safe.
//   - meterd_quota_e2e_test.go     (no tag)        boots apid + schedd +
//     meterd for the M7 "park within one tick" gate (issue #52).
//   - deploy_wake_metal_test.go    //go:build metal boots apid + schedd +
//     imaged + vmmd + gatewayd-internal.
//     Needs /dev/kvm and root.
//
// Per-daemon configuration:
//
//   - apid        env    FAAS_APID_LISTEN=127.0.0.1:<port>
//   - gatewayd-internal    env    FAAS_GATEWAY_LISTEN=127.0.0.1:<port>
//     FAAS_SCHEDD_SOCKET=<tmp>/schedd.sock
//     FAAS_APPS_DOMAIN=<test domain>
//   - imaged      env    FAAS_GUEST_INIT=<repo>/guest/init  (or empty)
//     FAAS_APPS_ROOT=<tmp>/apps
//     FAAS_OCI_INSECURE=1                  (test-only)
//   - schedd      toml   socket_path / vmmd_socket
//   - vmmd        toml   socket_path / kernel_path (metal tag only)
//   - builderd    toml   vmmd_socket / cache_dir / builder_base /
//     build_drive_dir / build_export_dir (issue #57 M6 e2e)
//
// FAAS_OCI_INSECURE swaps imaged's egress-guarded http.Client for a plain one
// so the fakeregistry on 127.0.0.1 is reachable. The guard denies loopback by
// design; this knob is for tests only (the WARN log makes that obvious).
//
// FAAS_SKIP_SOCKET_GROUP is set by the harness on every daemon so the
// shared unix socket (schedd.sock, vmmd.sock) binds even when the test
// host has no `faas` group. Production deploys always have the group —
// the ansible role creates it at bootstrap — so this knob is test-only.
// Without it, schedd errors with "wire: lookup gid for \"faas\": group:
// unknown group faas" on CI runners and dev Macs (issue #52 PR #59).
package e2etest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Harness owns one booted set of daemons + the shared PG pool. Stop() is
// registered via t.Cleanup so an assertion failure still tears everything down.
//
// Fields are exported for test consumption: H.APIDURL, H.GatewayURL, H.Pool.
type Harness struct {
	T                 *testing.T
	Pool              *pgxpool.Pool
	TmpDir            string
	BinDir            string
	SockDir           string // short-path unix-socket directory (see Start comment)
	APIDURL           string
	ScheddSock        string
	VMMDPath          string
	VMMDSock          string
	GatewayURL        string
	GatewayControlURL string // /metrics + /healthz, loopback only
	// RecoveryHMACKeyHex is a per-test 64-char hex string (32 bytes
	// when decoded) that the harness injects as FAAS_MFA_RECOVERY_HMAC_KEY
	// into every daemon's environment. Required because apid's
	// recovery-hmac loader (cmd/apid/main.go:loadOrGenerateRecoveryHMACKey)
	// REFUSES to boot without a key — there is no zero-key Warn fallback
	// like audit-hmac has. Tests that don't read recovery codes still
	// need a key wired or apid exits at boot with a recoverable-error
	// log line and the harness's waitTCP() times out. Populated in
	// Start / StartWithEnv; empty in tests that build their own
	// harness struct directly (which then must set it themselves or
	// accept the boot refusal).
	RecoveryHMACKeyHex string
	// HostHMACKeyPath is a per-test filesystem path to a 32-byte
	// file (mode 0o400) that apid loads via
	// FAAS_HOST_HMAC_KEY_PATH at startup (ADR-117 PR-C). apid
	// REFUSES to boot without this — see the rationale at
	// cmd/apid/main.go::loadHostHMACKey. The file is fresh per
	// test (crypto/rand) so two parallel tests don't share a
	// key, and so a leaked key from one test's debug output is
	// useless for the next test's value_hash discriminator. Path
	// populated in Start / StartWithEnv; tests that build their
	// own harness struct must set it themselves.
	HostHMACKeyPath string
	ImagedTmp       string // FAAS_APPS_ROOT
	BuilderdCfg     string // FAAS_BUILDERD_CONFIG path (issue #57 M6 e2e)

	// Per-daemon state. nil for a daemon not started (e.g. quota test skips
	// the metal-only daemons).
	procs []*exec.Cmd
}

// currentHarness points at the most recently booted Harness. Used by
// dumpProcs (called from waitUnix on timeout) to flush the live daemon
// stdout/stderr to the test log so a CI failure has the daemon's last
// words to bisect with. Single-active-harness is the only supported
// shape — cmd/e2e runs one test at a time per package. Set/cleared in
// Start + StartWithEnv.
var currentHarness *Harness

// snapshotProcs returns the active harness's running procs, or nil. Used
// by dumpProcs to keep waitUnix timeout debug logs self-contained.
func snapshotProcs() []*exec.Cmd {
	if currentHarness == nil {
		return nil
	}
	return currentHarness.procs
}

// Start brings up `which` daemons and wires readiness. Each daemon subprocess
// runs in its own goroutine draining stdout/stderr to a per-daemon buffer
// (logged on teardown so a flaky failure has the daemon's last words).
func Start(t *testing.T, pool *pgxpool.Pool, which Which) *Harness {
	t.Helper()

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("e2etest: mkdir bin: %v", err)
	}
	appsRoot := filepath.Join(tmp, "apps")
	if err := os.MkdirAll(appsRoot, 0o755); err != nil {
		t.Fatalf("e2etest: mkdir apps: %v", err)
	}

	// Socket dir lives outside t.TempDir() because macOS's t.TempDir() is
	// under /var/folders/.../T/<random> and a test name + random suffix
	// can exceed sun_path's 104-byte cap. /tmp is short and stable on
	// every runner; we own the directory exclusively so cleanup is just
	// an os.RemoveAll (registered via t.Cleanup).
	sockDir, err := os.MkdirTemp("", "faas-e2e-sock-*")
	if err != nil {
		t.Fatalf("e2etest: mkdir sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	h := &Harness{T: t, Pool: pool, TmpDir: tmp, BinDir: bin, ImagedTmp: appsRoot, SockDir: sockDir, RecoveryHMACKeyHex: newRecoveryHMACKeyHex(t), HostHMACKeyPath: newHostHMACKeyFile(t, tmp)}
	currentHarness = h

	// DB URL — pgtest opened the test pool with search_path=<schema>,public.
	// The daemon subprocess must use the SAME schema so its reads/writes
	// land where the test seeded rows. Append search_path as a connection
	// option (pgx accepts it both in DSN and via RuntimeParams).
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	if schema := pgtest.SchemaOf(pool); schema != "" {
		dbURL = injectSearchPath(dbURL, schema)
	}

	buildBinaries(t, bin)

	// Block until the schema is at the current migration target. The
	// meterd subprocess (issue #52) reads accounts on its first tick and
	// would otherwise race the migration — see cmd-e2e-schedd-migration-race.
	// See e2eMigrationTarget for the head value and the bump rationale.
	pgtest.WaitForMigration(t, pool, e2eMigrationTarget, 30*time.Second)

	if which&APID != 0 {
		startAPID(t, h, bin, dbURL)
	}

	if which&Schedd != 0 {
		sockPath := filepath.Join(h.SockDir, "schedd.sock")
		vmmdSock := filepath.Join(h.SockDir, "vmmd.sock")
		cfgPath := writeScheddConfig(t, h, tmp, which&Gatewayd != 0)
		signPubPath := writeScheddSignPub(t, h)
		env := append(testEnvCommon(dbURL),
			"FAAS_SCHEDD_CONFIG="+cfgPath,
			"FAAS_SIGN_PUB="+signPubPath,
		)
		h.procs = append(h.procs, startProc(t, bin, "schedd", env))
		h.ScheddSock = sockPath
		h.VMMDSock = vmmdSock
		// 30s tolerates schedd's first-boot db.MigrateUp on a fresh
		// schema — observed 16s on CI's postgres15 service for 12
		// migrations. The metal path reuses the same socket so this
		// ceiling also covers the post-migration cold start.
		waitUnix(t, sockPath, 30*time.Second)
		// Multi-host safety cluster PR-7 (audit F5) removed the
		// legacy FAAS_SCHEDD_SOCKET fallback in
		// pkg/gateway/pgbackend.go:resolveSched; the scheddrouter
		// now dials compute_nodes.schedd_target_url, which
		// migration 00090 seeds with the canonical production
		// socket (/run/faas/schedd.sock). Re-point the row at the
		// per-test socket so synth dispatch can find schedd.
		setDefaultLocalScheddTarget(t, pool, sockPath)
	}

	if which&VMMD != 0 {
		// Metal-only path. Caller is responsible for ensuring /dev/kvm + root.
		sockPath := h.VMMDSock
		if sockPath == "" {
			sockPath = filepath.Join(h.SockDir, "vmmd.sock")
			h.VMMDSock = sockPath
		}
		cfgPath := filepath.Join(tmp, "vmmd.toml")
		// FAAS_TEST_KERNEL matches the convention used by pkg/fcvm/manager_metal_test.go.
		kernelPath := os.Getenv("FAAS_TEST_KERNEL")
		if kernelPath == "" {
			kernelPath = "/srv/fc/base/vmlinux-6.1"
		}
		cfg := fmt.Sprintf(
			`socket_path = %q
owner_user = %q
kernel_path = %q
`,
			sockPath, "root", kernelPath,
		)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
			t.Fatalf("e2etest: write vmmd.toml: %v", err)
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_VMMD_CONFIG="+cfgPath,
		)
		h.procs = append(h.procs, startProc(t, bin, "vmmd", env))
		waitUnix(t, sockPath, 10*time.Second)
	}

	if which&Imaged != 0 {
		// guest/init lives at repo root in dev; tests don't run a real guest,
		// but imaged still wants the path. Use a placeholder file so its
		// existence check passes — the metal test will overwrite with the
		// real binary if it needs to.
		guestInit := os.Getenv("FAAS_GUEST_INIT")
		if guestInit == "" {
			guestInit = filepath.Join(tmp, "init")
			if err := os.WriteFile(guestInit, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatalf("e2etest: write placeholder guest init: %v", err)
			}
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_GUEST_INIT="+guestInit,
			"FAAS_APPS_ROOT="+appsRoot,
			"FAAS_OCI_INSECURE=1",
			"DATABASE_URL="+dbURL,
			"PATH="+os.Getenv("PATH"),
			"HOME="+os.Getenv("HOME"),
		)
		// Optional builder-base override (Lima / CI without ghcr creds). When
		// FAAS_TEST_BUILDER_BASE_REF is set, imaged pulls the base from there
		// instead of the production ghcr.io/poyrazk/builder-base:latest
		// (which 403s anonymously). FAAS_TEST_DEPLOY_BASE_REF, if set,
		// overrides the per-runtime base ref used by aboveBaseLayers at
		// deploy time so it also dials the stub registry. Default behavior
		// is unchanged.
		if ref := os.Getenv("FAAS_TEST_BUILDER_BASE_REF"); ref != "" {
			env = append(env, "FAAS_BUILDER_BASE_REF="+ref)
			if path := os.Getenv("FAAS_TEST_BUILDER_BASE_PATH"); path != "" {
				env = append(env, "FAAS_BUILDER_BASE_PATH="+path)
			}
		}
		if dbr := os.Getenv("FAAS_TEST_DEPLOY_BASE_REF"); dbr != "" {
			env = append(env, "FAAS_TEST_DEPLOY_BASE_REF="+dbr)
		}
		h.procs = append(h.procs, startProc(t, bin, "imaged", env))
	}

	if which&Gatewayd != 0 {
		startGatewayd(t, h, bin, dbURL, nil)
	}

	if which&Meterd != 0 {
		startMeterd(t, h, bin, dbURL)
	}
	if which&Builderd != 0 {
		// Issue #57: builderd participates in the M6 orchestrator e2e.
		// It subscribes to build_queued on the same Postgres the harness
		// uses, then asks vmmd to cold-boot a builder microVM. The
		// per-test config redirects cache_dir, build_drive_dir, and
		// build_export_dir into <tmp> so two parallel runs never collide
		// (each t.TempDir is unique per test process). Without the env
		// override (FAAS_BUILDERD_CONFIG, cmd/builderd/main.go), the
		// daemon would load /etc/faas/builderd.toml and write into the
		// host's production dirs.
		cfgPath := filepath.Join(tmp, "builderd.toml")
		vmmdSock := h.VMMDSock
		if vmmdSock == "" {
			vmmdSock = "/run/faas/vmmd.sock" // matches builderd default
		}
		cfg := fmt.Sprintf(
			`vmmd_socket = %q
cache_dir = %q
builder_base = %q
build_drive_dir = %q
build_export_dir = %q
`,
			vmmdSock,
			filepath.Join(tmp, "cache"),
			envBuilderBase(t),
			filepath.Join(tmp, "drive"),
			filepath.Join(tmp, "out"),
		)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
			t.Fatalf("e2etest: write builderd.toml: %v", err)
		}
		env := []string{
			"FAAS_BUILDERD_CONFIG=" + cfgPath,
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		if dbr := os.Getenv("FAAS_TEST_DEPLOY_BASE_REF"); dbr != "" {
			env = append(env, "FAAS_TEST_DEPLOY_BASE_REF="+dbr)
		}
		h.procs = append(h.procs, startProc(t, bin, "builderd", env))
		h.BuilderdCfg = cfgPath
		// builderd doesn't expose a TCP/unix listener (it's a pg_notify-
		// driven orchestrator). imaged has the same shape and the harness
		// already relies on the same "no wait, daemon self-asserts readiness
		// in its first log line" pattern. giveSubscribedToBuildQueued polls
		// pg_stat_activity for a session holding LISTEN build_queued in this
		// schema — the only consumer of that channel is builderd.
		if err := h.waitBuilderdListens(10 * time.Second); err != nil {
			t.Fatalf("e2etest: builderd did not subscribe to build_queued: %v", err)
		}
	}

	t.Cleanup(h.stop)
	return h
}

// Which flags select which daemons to boot. Bitmask so a test can ask for
// just apid (quota) or all seven (M6 metal + M7 meterd).
type Which int

const (
	APID Which = 1 << iota
	Schedd
	VMMD
	Imaged
	Gatewayd
	Meterd
	Builderd
)

// DeployWake is the daemon set the image deploy → snapshot → park → wake
// acceptance requires. The path never queues a build, so Builderd is
// excluded — the test starts faster and the failure surface is smaller.
const DeployWake = APID | Schedd | VMMD | Imaged | Gatewayd

// All is the full metal set (DeployWake + Meterd + Builderd). Used by tests that
// exercise the build pipeline (TestBuildMetal and friends) where the
// builderd daemon must actually queue and serve a build, and by the M7 meterd
// tests that need the quota-meter writer running.
const All = DeployWake | Meterd | Builderd

const testDomain = "apps.test.example"

// e2eMigrationTarget is the migration head every e2e harness boot waits
// for. Centralised here so the next migration land only touches this one
// constant (Start and StartWithEnv both reference it). The value MUST
// match the highest NNNNN_<name>.sql filename under migrations/. The
// per-plan tests that need a tighter target (e.g. meterd_quota_e2e)
// call pgtest.WaitForMigration with their own N and remain green.
//
// Bumped 83 → 86 → 93 → 94 → 102 → 118 → 120 → 134 → 136 across twelve rebase cycles:
//
// (issue #461 / ADR-064 — registry_credentials landing on slot 94
// after PR #529 (Tier A4 cross-node app rebalance, ADR-064) raced
// from 92 to 93 between my pushes, holding both 92 and 93 as real
// migrations on its branch. 92 stays as a reservation fence here so
// the embedded FS stays contiguous from 1..94 without a 92 gap.
// Bumped 83 → 87 → 92 → 93 → 94 across four rebase cycles; the
// renumber chain tracks the gate's "next free slot past the live
// head" rule when sibling PRs race for the same N.)
//
//   - 94 → 110 after PR #533 (Tier A5 cross-node live-instance
//     migration, ADR-066) merged real migrations at 00103 +
//     00104, then PR #536 (iam-6 personal-org backfill) merged
//     real at 00105, then PR #525 (issue #470 M8 warm snapshot)
//     merged real at 00109 + 00110, then PR #539 (iam-5 key
//     expiry) merged real at 00115 (renumbered 00106 → 00115 on
//     its branch), then PR #538 (Tier A6 migrating watchdog)
//     merged real at 00114, then PR #532 (issue #517 PR-C)
//     merged real at 00107. The renumber commits in this
//     branch's history (101/102 → 103/104 → 105/106 → 107/108 →
//     109/110) collided with main's new files, so all five were
//     dropped on rebase (the work is now collapsed into a single
//     post-rebase commit). The branch renumbered 00101 → 00109 +
//     00102 → 00110 past main's new head at 105; the 106-108 gap
//     is filled by reserve_slot.sql fences at 00106/00107/00108
//     per ADR-041 so the embedded FS stays contiguous 1..110.
//     (Slot 00105 is owned by main's 00105_personal_org_backfill.sql
//     — no fence needed there.) Open PRs claim: 00101 (PR #531
//     issue #463 sidecars), 00107 (PR #532 issue #517 PR-C
//     canonical wake timeline) — none overlap with 00109/00110.
//
//   - 110 → 118 after PR #531 (issue #463 sidecars) merged real
//     at 00118_deployments_sidecars.sql. The PR was the first
//     sibling to take the slot past 110; the renumber chain
//     110 → 111 → 112 on this branch + 113/114/115/116/117
//     reserve_slot fences collapsed into a single 110 → 118
//     jump. The 111-117 gaps are filled by main's reserve_slot
//     fences at 00111/00112/00113/00116/00117 (slot 115 was
//     real — 00115_api_key_expiry_rotation.sql; slot 114 was
//     real — 00114_events_wake_id_idx.sql).
//
//   - 118 → 120 → 122 on the final two rebases onto main
//     (1768ed4b) for PR #543 (issue #470 / PR #470-FU-B,
//     framework_ready signal). The renumber chain (112 → 116 →
//     117 → 118 → 119 → 120) tracked sibling PRs racing for the
//     same N (#540 webhook, #531 sidecars) but on the first
//     rebase all five renumber commits + their reserve_slot
//     fences collapsed into a single 112 → 120 jump. The 119
//     gap is filled by 00119_reserve_slot.sql per ADR-041 so
//     the embedded FS stays contiguous 1..120. PR #543 held
//     the 119 fence on that branch; PR #531 claimed 118 (now
//     main's real migration 00118_deployments_sidecars.sql —
//     fence dropped on merge).
//
//     After the first rebase merged (slot 120 live on the
//     branch), PR #547 (Tier A7 gatewayd-internal split) opened against
//     main claiming slots 00119/00120/00121. The cross-PR slot
//     gate then rejected PR #543's push with "migration slot
//     00120 is also claimed by open PR #547", forcing a second
//     renumber: 120 → 122. The 119 fence (already on this
//     branch from the previous cycle) was renamed 119 → 121 to
//     keep the embedded FS contiguous 1..122 while PR #547's
//     00119_reserve_slot.sql + 00120_warm_hint.sql occupy slots
//     119 + 120 on main. Whichever side lands first, the other
//     drops its reservation on rebase. Slot 122 adds the
//     `instances.framework_ready_at TIMESTAMPTZ NULL` column
//     that vmmd's `FrameworkReady` gRPC handler writes to when
//     the guest-init signals via vsock DGRAM port 1027 (msg=4).
//     Slots 127/128 (issue #463 / ADR-069 / PR-B) add the
//     `deployment_sidecar_layers` table and the
//     `events_sidecar_name_idx` partial expression index.
//     Slots 131/132/133 (issue #557 / ADR-072, PR #618) add the
//     apps.min_instances + instances.app_id/deployment_id + deployments
//     .min_instances columns + checks/indexes for the per-deployment
//     floor reconciler. Slots 129 + 130 are fenced on main per ADR-041
//     to keep the embedded set contiguous 1..133 while the cross-PR
//     collision detector excludes the reservations. Slot 134 (issue
//     #190 / ADR-061 / PR-6) flips api_keys.org_id to NOT NULL after
//     the personal-org backfill.
//
//     Slot 138 (issue #475 / ADR-075) adds apps.eviction_priority
//     (text NOT NULL DEFAULT 'best_effort', CHECK
//     apps_eviction_priority_chk) so the schedd reaper can tier
//     apps between cross-account RAM-pressure eviction. Slot 135
//     is held open by a fence on this branch because four sibling
//     PRs (#540 webhook-deliveries, #651 deploy-scans, #653 IAM
//     provenance, #654 per-deployment authn) all touched slot 135
//     in their first commits; this PR renumbered 135→138 after the
//     cross-PR slot gate surfaced the cluster. Whichever of #540/
//     #651/#653/#654 lands first, the fences resolve themselves
//     (real schema shadows the fence on the side that merges last).
//
//   - 138 → 145 with PR #653 (IAM hardening mega-PR). The mega-PR
//     adds slot 00144_api_keys_provenance.sql (logical change 2:
//     api_keys.created_ip / created_ua / parent_key_id — the SOC2
//     audit-evidence lineage) and slot 00145_sessions_binding.sql
//     (logical change 5: sessions.binding_hash for the stolen-cookie
//     auto-revoke cross-check). Originally landed at 135/136, then
//     renumbered to 139/140 after PR #647 (issue #475 / ADR-075
//     eviction priority) merged at slot 138 with reserve_slot
//     fences at 135/136/137, then renumbered a second time to
//     141/142 after the cross-PR slot gate surfaced that open PRs #651 (deploy-scans) and #654 (per-deployment authn) also claimed slot 139, then renumbered a third time to 144/145 after PR #658 (issue #476 webhook delivery) claimed slots 140 + 141
//     PRs #651 (deploy-scans) and #654 (per-deployment authn) also
//     claimed slot 139. The four fence files
//     (00135/00136/00137/00139_reserve_slot.sql) on this branch are
//     byte-for-byte identical to the fences on main (139 is a new
//     fence added in this cycle to fill the slot vacated by the
//     second renumber). They hold the carved-out slots while this
//     branch sits past the eviction_priority land and get
//     `git rm`'d on merge.
//
//   - 145 → 157 with PR #697 (issue #554 / ADR-079 follow-up), which
//     adds 00157_deployments_parked_reason.sql (parked_reason +
//     parked_at + closed-set CHECK). Originally landed at 155; renumbered
//     to 157 after main's PR #676 (issue #676 raw bridge) shipped the
//     real 00155_apps_websocket_enabled.sql, then to 156 with a 156
//     fence to dodge PR #698's 00156_apps_auth_default_flip, then back
//     to 157 once PR #698 merged into main at 16:21:08 with
//     00156_apps_auth_default_flip.sql — the 156 slot became a real
//     main-landed migration and PR #697 picks 157 as the next free
//     slot above main's head. The renumber chain is the standard
//     PR-#697 follow-up to the PR-#653 145 chain.
//
// PR-C: bumped 216 → 237 for the maintenance cluster
// (00236_edge_rules_kind_maintenance.sql +
// 00237_apps_maintenance_mode.sql). The previous target (228)
// made `pgtest.WaitForMigration` return early because the
// schema head was already past 228 — every `cmd/e2e` test would
// silently skip for the entire maintenance cluster. 237 is
// chosen as "next free integer above main's real head" so a
// future migration merely bumps this constant again. The
// discipline (memory: cross-pr-slot-gate-fence-pattern) is that
// the only line a migration land touches in this file is this
// constant + the doc-comment history above.
//
// PR #882 ADR-098 §9.A connection-aware execution PR-A re-bumped
// 226 → 229 — slot 226 is the data_upstreams + data_upstream_probes
// schema (real DDL, landed via PR-A's `feat(migration): ADR-098
// §9.A`); the run-up from 226 to 229 happened because main absorbed
// PR #845's 00229_edge_rules_kind_geo.sql + sibling PRs #864/#867
// in the interim (see the chain below). PR-A's own renumber story
// was the reverse: PR-0 fenced 226 from PR-0 (issue # PR #858,
// ADR-098 §renumber) and PR-A replaced the fence with the real DDL
// on top of (still 00221 → 00226) renumbers.
//
// PR #845 (kind=geo, ADR-091 D21) + PR #863 (ADR-096 PR-A
// app_errors) + PR #866 (ADR-091 D20-D25 cors_defaults) + PR #864
// (reqbudget PR1, ADR-093) + PR #867 (maintenance PR-A, ADR-091
// amendment): all five bumped 215/216 → 229 in flight. The renumber
// chain tracks the gate's "next free slot past the live head AND
// past open-PR claims" rule when sibling PRs race for the same N.
//
//   - 215 → 229 after PR #844 (ADR-093 per-route app metrics)
//     landed 00216_apps_route_metrics_enabled.sql, after
//     PR #855 (ADR-091 D24 kind=limit) landed
//     00219_edge_rules_kind_limit.sql, after PR #851
//     (issue-272 PR-preview environments) landed
//     00220_preview_app_columns.sql, after PR #854
//     (ADR-095 scale-to-zero T1 single-flight + phase
//     telemetry) claimed 00221_instances_request_count.sql,
//     after PR #863 (ADR-096 PR-A app_errors) landed
//     00222_app_errors.sql, after PR #866 (ADR-091
//     D20-D25 cors_defaults) landed 00224_apps_cors_defaults.sql,
//     and after open-PR stampede with PR #864 (reqbudget PR1
//     claiming 00226), PR #867 (maintenance PR-A claiming 00227
//     kind=maintenance + 00228 apps.maintenance_mode), and PR
//     #873 (cli-secret-scan fencing 223-227) — which pushed
//     PR #845's kind=geo from 00220 → 00221 → 00222 → 00223 →
//     00226 → 00229.
//   - 215/216 → 229 by ADR-096 PR-A + ADR-091 D20-D25
//   - open-PR stampede (app_errors schema at 00222, leaving
//     00223 free for PR #845; PR #866's CORS team then placed a
//     coexistence fence at 00223 alongside their own real
//     migration at 00224, so PR #845 renumbered to 00226 — but
//     PR #864 (reqbudget PR1) ALSO claimed 00226 with a real
//     schema, so PR #845 stepped past PR #864 + PR #867's
//     00227/00228 to the next free slot 00229). The in-flight
//     cross-PR fences at 215..228 sit between main's 214
//     (edge-rules-kind=validate) and 229 (kind=geo): 215
//     compute_node_heartbeats_stats (PR #851), 216
//     apps_route_metrics_enabled (PR #860), 217 ADR-092
//     app_secrets_scope (PR #849), 218 preview-envs (PR
//     preview-envs ADR-098 per PR #876), 219 edge_rules_kind_limit (PR
//     kind=validate PR-C), 220 preview_app_columns (PR preview
//     envs), 221 ADR-096 reserve fence for slot 222 itself, 222
//     app_errors (PR #863), 223 PR #866 coexistence fence
//     (passed-through by PR #845, now historical), 224
//     apps_cors_defaults (PR #866), 225 PR #866 reserve fence,
//     226 PR #864 real (reqbudget) + PR #858 fence, 227
//     PR #867 real (kind=maintenance) + PR #873 fence, and
//     228 PR #867 real (apps.maintenance_mode).
//
// PR-C renumber history (8 cycles) on top of main's
// 00220_preview_app_columns + 00221_instances_request_count
// (PR #851 + PR #854 wake single-flight) + 00222_app_errors
// (PR #863 ADR-096 PR-A) + 00223..00226 fences (PR #866 CORS
// cluster + ADR-098 PR-0) + 00227..00228/00229..00230 fences
// (PR #845 kind=geo added 00229 + 00230 fence during the
// kind=geo/maintenance cross-PR stampede):
//
//	00220/00221 → 00222/00223 → 00224/00225 → 00227/00228 →
//	00231/00232 → 00234/00235 → 00236/00237 (final; main absorbed
//	00220..00226, then PR #845 added 00227/00228/00229/00230
//	(00229 real kind=geo, rest fences), and after the renumber
//	to 00231/00232 the cross-PR slot precheck on push caught a
//	collision with open PR #864 (ADR-093 request budgets claims
//	00232) and PR #873 (secretscan v2 fences 00231/00232/00233),
//	stepping to 00234/00235; then after PR #872 landed (and
//	PR #882 data_upstreams opened), PR #864 reshuffled to 00234
//	re-claiming our slot — cross-PR slot precheck tripped again,
//	kind=maintenance cluster stepped to 00236/00237 to dodge the
//	reshuffle).
//
// The 00217 + 00218 slots carry `*_reserve_slot.sql` fences
// for PR #849 (ADR-092 PR-A app_secrets.scope) and PR #845's
// own fence respectively; the 00221 fence is PR #863's
// ADR-096 reservation; the 00223 fence is PR #866's
// coexistence marker (passed-through by PR #845's renumber,
// kept as a no-op so the contiguity gate doesn't trip on the
// renumber chain).
//
// PR-B (this PR, #875, ADR-096 handlers + SDK + OpenAPI) adds
// NO migration; the head stays at 237 against main at merge.
// PR-C (e2e + ADR-096 docs) will need its own slot fence —
// pre-check via `gh api .../contents/migrations?ref=main`
// before opening the PR per the cross-PR slot precheck pattern.
//
// Triggers-mega audit #10: bumped 237 → 275 → 287 → 290 → 296 → 297/298/299
// → 305 → 308 → 309 → 313 → 314 across the post-rebase renumber chain. The
// BrokerPoisonStrategy migration is 00299_triggers_poison_strategy.sql,
// payload_max is 00298, and the unified triggers schema is 00297.
// PR #986's ADR-120 domain doctor claims 00313_domain_doctor_observations.sql
// (rebumped 00296 → 00309 → 00305 → 00308 → 00309 → 00313 — the cross-PR
// slot precheck CI caught 00305 colliding with PR #984/992, 00308 colliding
// with PR #999, and 00309 colliding with PR #990 (app_secret_value_hash)
// + PR #1000 (consumer_keys); PR #990 fences 00310-00312 in its own
// renumber chain, leaving 00313 as the first free slot above all
// open PR fences). Issue #977 / ADR-116 deployment annotations rebump
// 00288 → 00346 (00310-00312 fenced by PR #990; 00313 taken by #986;
// 00314 + 00315 taken by #990/#991; PR #999 fences 00326 (apps_public_auth_ip_allowlist; merged to main); 00329 taken by #1000 consumer_keys; 00333 fence by #1005; PR #1004 kinesis/documentdb 00334; PR #1010 repair_app_secrets_scope 00341; 00342 is the first free slot above #1010 repair's 00338 fence line). The discipline
// (memory: cross-pr-slot-gate-fence-pattern) is that the only line a
// migration land touches in this file is this constant + the doc-comment
// history above.
//
// PR #990 (ADR-117 PR-C, env-diff matrix) bumped 313 → 327 → 340 → 347 → 352 → 356 → 357 → 387 → 410
// across fourteen rebump rounds. The value_hash column lands at
// 00410_app_secret_value_hash.sql (HMAC-SHA256 of plaintext, truncated
// to 16 hex — the trustworthy value-equality discriminator for the
// GET /v1/apps/{slug}/env-diff matrix). Slot rebump chain
// 291 → 296 → 303 → 309 → 314 → 321 → 322 → 327 → 340 → 347 → 352 →
// 356 → 357 → 387 → 410 → 416. Round 15 forced by open PRs claiming
// real schemas at 00387 (PR #1036 compute_nodes_active_unique),
// 00388-00390 (PR #1036 instances_wake_attempt_active_unique /
// migration_notify / cluster_signing_keys), 00409 (origin/main's
// 00409_reserve_slot.sql fence from PR-C rebump), 00411-00412
// (PR #1017 alert_presets, PR #1024 deployments_priority, PR #1064
// deployment_audit), 00413-00415 (PR #1017 alert_presets), and 00416
// (PR #1049 openapi_import rebump). Round 15 added 5 fences at
// 00411-00415 to bridge alphasort positions 411-415; my real lands at
// position 416 = version 416. Past 00416 is currently clear of real
// claims.
const e2eMigrationTarget = 416

// StartWithEnv is the G2-aware entrypoint used by the secrets e2e:
// the test wants apid to load a specific host.age.pub (FAAS_HOST_AGE_
// RECIPIENT_PATH) so it can seal. StartWithEnv boots JUST apid — not
// the metal-only daemons — with the extra env appended.
//
// Use this when the test isn't metal and only needs apid under
// configuration control (which is most of the quota-style e2es; quota
// only needs apid, no schedd/vmmd).
func StartWithEnv(t *testing.T, pool *pgxpool.Pool, which Which, extraEnv []string) *Harness {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("e2etest: mkdir bin: %v", err)
	}
	appsRoot := filepath.Join(tmp, "apps")
	if err := os.MkdirAll(appsRoot, 0o755); err != nil {
		t.Fatalf("e2etest: mkdir apps: %v", err)
	}
	// See Start for why sockDir lives outside t.TempDir() — macOS sun_path
	// limit, and `/tmp/faas-e2e-sock-*` is short and stable everywhere.
	sockDir, err := os.MkdirTemp("", "faas-e2e-sock-*")
	if err != nil {
		t.Fatalf("e2etest: mkdir sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	h := &Harness{T: t, Pool: pool, TmpDir: tmp, BinDir: bin, ImagedTmp: appsRoot, SockDir: sockDir, RecoveryHMACKeyHex: newRecoveryHMACKeyHex(t), HostHMACKeyPath: newHostHMACKeyFile(t, tmp)}
	currentHarness = h

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	if schema := pgtest.SchemaOf(pool); schema != "" {
		dbURL = injectSearchPath(dbURL, schema)
	}
	buildBinaries(t, bin)

	// Gate every daemon launch on the schema arriving at the current
	// migration target. Without this, meterd's first tick races the
	// migration (issue #52 acceptance race; see
	// cmd-e2e-schedd-migration-race memory). See e2eMigrationTarget
	// for the head value and bump history.
	pgtest.WaitForMigration(t, pool, e2eMigrationTarget, 30*time.Second)

	if which&APID != 0 {
		addr := freeTCPAddr(t)
		// Empty FAAS_APID_METRICS_ADDR disables the metrics listener
		// (cmd/apid/main.go:425). Without this override the daemon
		// tries to bind 127.0.0.1:9101 and — when a prior apid run
		// (or a sibling daemon from a different test) is still
		// holding the port — exits with `bind: address already in use`
		// before the main HTTP listener is reached. The e2e harness
		// doesn't scrape /metrics; the scrape observer is still wired
		// into the main mux so the dashboard panels stay accurate.
		// Per-test FAAS_SPOOL_ROOT + FAAS_SCAN_SPOOL_ROOT — see
		// startAPID in Start for the rationale.
		spoolRoot := filepath.Join(h.TmpDir, "spool")
		scanRoot := filepath.Join(h.TmpDir, "scan-spool")
		for _, d := range []string{spoolRoot, scanRoot} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("e2etest: mkdir spool: %v", err)
			}
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_APID_LISTEN="+addr,
			"FAAS_APPS_DOMAIN="+testDomain,
			"FAAS_APID_METRICS_ADDR=",
			"FAAS_SPOOL_ROOT="+spoolRoot,
			"FAAS_SCAN_SPOOL_ROOT="+scanRoot,
		)
		env = append(env, extraEnv...)
		h.procs = append(h.procs, startProc(t, bin, "apid", env))
		h.APIDURL = "http://" + addr
		waitTCP(t, addr, 10*time.Second)
	}
	if which&Schedd != 0 {
		sockPath := filepath.Join(h.SockDir, "schedd.sock")
		vmmdSock := filepath.Join(h.SockDir, "vmmd.sock")
		cfgPath := writeScheddConfig(t, h, tmp, which&Gatewayd != 0)
		signPubPath := writeScheddSignPub(t, h)
		env := append(testEnvCommon(dbURL),
			"FAAS_SCHEDD_CONFIG="+cfgPath,
			"FAAS_SIGN_PUB="+signPubPath,
		)
		env = append(env, extraEnv...)
		h.procs = append(h.procs, startProc(t, bin, "schedd", env))
		h.ScheddSock = sockPath
		h.VMMDSock = vmmdSock
		// 30s tolerates schedd's first-boot db.MigrateUp (same
		// rationale as the Start path above).
		waitUnix(t, sockPath, 30*time.Second)
		// Multi-host safety cluster PR-7 (audit F5) removed the
		// legacy FAAS_SCHEDD_SOCKET fallback in
		// pkg/gateway/pgbackend.go:resolveSched; the scheddrouter
		// now dials compute_nodes.schedd_target_url, which
		// migration 00090 seeds with the canonical production
		// socket (/run/faas/schedd.sock). Re-point the row at the
		// per-test socket so synth dispatch can find schedd.
		setDefaultLocalScheddTarget(t, pool, sockPath)
	}
	if which&Meterd != 0 {
		startMeterd(t, h, bin, dbURL, extraEnv)
	}
	if which&Gatewayd != 0 {
		startGatewayd(t, h, bin, dbURL, extraEnv)
	}
	t.Cleanup(h.stop)
	return h
}

// startAPID boots apid under Start()'s shared schedule. Kept for the
// inner-loop case where Start() already handled the other daemons but
// apid wasn't part of the Which mask (the existing quota_e2e relies on
// this — Start with APID and no extras is fine).
func startAPID(t *testing.T, h *Harness, bin, dbURL string) {
	t.Helper()
	addr := freeTCPAddr(t)
	// Per-test spool roots. Without per-test FAAS_SPOOL_ROOT +
	// FAAS_SCAN_SPOOL_ROOT, every test writes to the system-wide default
	// (/var/spool/faas/builds) and concurrent tests in the same
	// package race each other to mkdir the same parent — yielding 503
	// capacity_unavailable "could not create spool dir" in
	// cmd/apid/extract.go and cmd/apid/deploy_inputs.go. (Go runs
	// tests in the same package serially unless -parallel is set, but
	// once any package-level parallelism is introduced, the per-test
	// temp dirs keep behaviour stable.)
	spoolRoot := filepath.Join(h.TmpDir, "spool")
	scanRoot := filepath.Join(h.TmpDir, "scan-spool")
	for _, d := range []string{spoolRoot, scanRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("e2etest: mkdir spool: %v", err)
		}
	}
	env := append(testEnvCommon(dbURL),
		"FAAS_APID_LISTEN="+addr,
		"FAAS_APPS_DOMAIN="+testDomain,
		"FAAS_SPOOL_ROOT="+spoolRoot,
		"FAAS_SCAN_SPOOL_ROOT="+scanRoot,
	)
	h.procs = append(h.procs, startProc(t, bin, "apid", env))
	h.APIDURL = "http://" + addr
	waitTCP(t, addr, 10*time.Second)
}

// writeScheddConfig renders the per-test schedd.toml and writes it under
// tmp. Returns the path to the file. The gateway_synth_socket line is
// emitted when includeSynth is true so the drain goroutine actually
// subscribes to invocation_due; an empty value silently disables the
// drain (cmd/schedd/main.go:319-345) which is the correct shape for
// tests that don't exercise async-invoke. Centralised so a future env
// var or config knob lands in one place instead of two (Start vs
// StartWithEnv had identical templates before PR #218).
//
// gateway_metrics_url is intentionally empty: schedd is started
// BEFORE gatewayd-internal in Start() (the schedd first-boot
// migration runs while gatewayd-internal is still booting), so the control
// plane address isn't known yet. With an empty URL, schedd's
// scaleup trigger is disabled (cmd/schedd/config.go:110-118,
// issue #169 / #172). Boot path is unaffected — schedd logs a
// single warn when the trigger ticks, which it doesn't.
func writeScheddConfig(t *testing.T, h *Harness, tmp string, includeSynth bool) string {
	t.Helper()
	sockPath := filepath.Join(h.SockDir, "schedd.sock")
	vmmdSock := filepath.Join(h.SockDir, "vmmd.sock")
	gatewaySynth := ""
	if includeSynth {
		gatewaySynth = filepath.Join(h.SockDir, "gatewayd-internal.sock")
	}
	cfgPath := filepath.Join(tmp, "schedd.toml")
	cfg := fmt.Sprintf(
		`socket_path = %q
owner_user = %q
vmmd_socket = %q
gateway_synth_socket = %q
gateway_metrics_url = ""
`,
		sockPath, "root", vmmdSock, gatewaySynth,
	)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("e2etest: write schedd.toml: %v", err)
	}
	return cfgPath
}

// startGatewayd boots gatewayd-internal (Tier A7 PR-B+) against the
// per-test schedd + apid + PG schema. The legacy 'gatewayd' binary is
// gone (its source moved into cmd/gatewayd-internal/ in PR-A); the
// single binary now serves the real routing + wake + proxy chain.
//
// Per-test setup:
//
//   - FAAS_GATEWAYD_CONFIG points at a freshly written TOML with the
//     public_addr, control_addr, apid_loopback fields. PublicAddr +
//     ControlAddr are free ports reserved via freeTCPAddr (no
//     collisions across tests in the same package).
//   - FAAS_GATEWAY_SYNTH_SOCKET is the unix socket path schedd's
//     drain goroutine dials to forward invocation_due envelopes
//     (cmd/schedd/main.go:319-345). Without the listener, schedd's
//     drain is silently disabled and async-invoke tests deadlock
//     waiting for state='completed'.
//   - FAAS_GATEWAY_LISTEN binds the per-test public HTTP port. Tests
//     that hit h.GatewayURL over plain HTTP now reach the real
//     handler chain (no more TEMPLATE_OK banner — that's the PR-A
//     gap that PR-B closes).
//   - FAAS_GATEWAY_CONTROL_LISTEN binds the loopback control plane
//     (/healthz, /readyz, /metrics).
//   - FAAS_SKIP_SOCKET_GROUP=1 suppresses wire.ListenOrRecreateByName's
//     group-faas lookup (every CI runner and dev Mac lacks the group;
//     production has it via the ansible role).
//   - FAAS_SCHEDD_SOCKET and FAAS_APPS_DOMAIN mirror what schedd
//     already expects; see writeScheddConfig for the dual.
//
// extraEnv is appended last so a test can inject extra knobs (none
// today; mirrors startMeterd's signature).
func startGatewayd(t *testing.T, h *Harness, bin, dbURL string, extraEnv []string) {
	t.Helper()
	if h.SockDir == "" {
		h.SockDir = filepath.Join(h.TmpDir, "socks")
	}
	addr := freeTCPAddr(t)
	controlAddr := freeTCPAddr(t)
	if h.ScheddSock == "" {
		h.ScheddSock = filepath.Join(h.SockDir, "schedd.sock")
	}
	apidLoopback := h.APIDURL
	if apidLoopback == "" {
		apidLoopback = "http://127.0.0.1:8081"
	}
	gwCfg := filepath.Join(t.TempDir(), "gatewayd.toml")
	if err := os.WriteFile(gwCfg, []byte(
		fmt.Sprintf("public_addr=%q\ncontrol_addr=%q\napid_loopback=%q\n",
			addr, controlAddr, apidLoopback),
	), 0o600); err != nil {
		t.Fatalf("e2etest: write gatewayd.toml: %v", err)
	}
	synthSock := filepath.Join(h.SockDir, "gatewayd-internal.sock")
	env := append(testEnvCommon(dbURL),
		"FAAS_GATEWAY_LISTEN="+addr,
		"FAAS_GATEWAYD_CONFIG="+gwCfg,
		"FAAS_GATEWAY_CONTROL_LISTEN="+controlAddr,
		"FAAS_GATEWAY_SYNTH_SOCKET="+synthSock,
		"FAAS_SCHEDD_SOCKET="+h.ScheddSock,
		"FAAS_APPS_DOMAIN="+testDomain,
	)
	env = append(env, extraEnv...)
	h.procs = append(h.procs, startProc(t, bin, "gatewayd-internal", env))
	h.GatewayURL = "http://" + addr
	h.GatewayControlURL = "http://" + controlAddr
	waitTCP(t, controlAddr, 10*time.Second)
}

// testEnvCommon returns the env every daemon gets in the harness:
//   - DATABASE_URL  (per-test schema via search_path injection in the
//     caller; the daemon's pool therefore targets the same schema the
//     test seeded rows in)
//   - FAAS_SKIP_SOCKET_GROUP=1 — see package doc comment. Without it,
//     the daemon's wire.ListenOrRecreateByName errors on a host without
//     the `faas` group, which is every CI runner and dev Mac. Production
//     deploys have the group; the ansible role creates it at bootstrap.
//   - FAAS_APP_ERRORS_ENABLED=false — ADR-096 / PR-B flipped the
//     kill-switch to default-on in cmd/apid/main.go, which now tries
//     to bind /run/faas/app_errors.sock in runAppErrorsServer on every
//     apid boot. The CI runner / dev Mac lacks the `faas-apid` user
//     that the listener boot probes (config.go:144-149); the lookup
//     returns an error and the apid never reaches the main HTTP
//     listener, so e2e `waitTCP(t, addr, 10s)` exhausts and every
//     test reports "did not accept within 10s". Production deploys
//     have the user (the systemd unit runs as `faas-apid`); the e2e
//     harness sets this off so the apid skips the new gRPC listener
//     and the main HTTP path boots cleanly. The reader-path handlers
//     (cmd/apid/handlers_app_errors.go) are not affected — they read
//     from the SQL store regardless of the gRPC listener state.
//   - FAAS_MFA_RECOVERY_HMAC_KEY=<per-test hex> — see Harness
//     .RecoveryHMACKeyHex. apid refuses to boot without a recovery
//     HMAC key (cmd/apid/main.go:loadOrGenerateRecoveryHMACKey); the
//     audit-hmac Warn-and-zero-key fallback that previously let the
//     e2e harness boot apid with no secrets at all does NOT apply
//     here. We generate a fresh per-test key so two tests running
//     in parallel don't share a key (and so a leaked key from one
//     test's debug output is useless for the next test's recovery
//     codes). The key is in-memory only — the test never persists
//     it to disk — so a reboot of the daemon in the middle of the
//     test is unsupported (matches production "key stable across
//     boot" semantics only if the operator persists it).
//   - PATH / HOME inherited so go-built daemons can `exec.LookPath`
//     helpers (notably firecracker, which schedd warns about but does
//     not require for the meterd quota gate).
//   - FAAS_HOST_HMAC_KEY_PATH=<per-test 0o400 file> — see Harness
//     .HostHMACKeyPath. ADR-117 PR-C widens the sealed envelope with
//     a 16-hex value_hash (HMAC-SHA256 over plaintext, keyed by a
//     per-host 32-byte key). apid refuses to start without this file
//     present (cmd/apid/main.go::loadHostHMACKey); the harness writes
//     a fresh crypto/rand-32-byte file at 0o400 in the per-test
//     TmpDir so the e2e path exercises the production posture
//     (missing/mode-wrong → refuse-to-start). Parallel tests get
//     independent keys.
func testEnvCommon(dbURL string) []string {
	env := []string{
		"DATABASE_URL=" + dbURL,
		"FAAS_SKIP_SOCKET_GROUP=1",
		"FAAS_APP_ERRORS_ENABLED=false",
		// ADR-115 D5 / PR #1191 C2: pkg/mail/factory refuses to boot
		// when FAAS_MAIL_TRANSPORT is unset on a non-dev box. Every
		// e2e boot is a dev box for this purpose; without the
		// override the daemon exits with ErrMailUnsetInProd before
		// the listener binds. Explicit FAAS_DEV=1 is the same
		// escape hatch the spec's CI box uses, and keeps the
		// contract visible in the harness (rather than papering
		// over it by setting FAAS_MAIL_TRANSPORT=log here).
		"FAAS_DEV=1",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		// E2E daemons use the Paddle placeholder credentials below and
		// must opt out of the production Polar default explicitly.
		"FAAS_BILLING_PROVIDER=paddle",
		// PR #962 CRIT-2: paddle.NewProvider rejects empty / whitespace
		// apiKey at construction. The e2e harness boots apid + meterd
		// via startProc; the boot path is daemon-real (exec.Cmd, not
		// deps injection). Without a sandbox-shaped key here, every
		// boot fatals with "cannot push usage without apiKey" and
		// every test that waits for a listener bound reports a
		// misleading "port not bound" timeout. The pdl_* placeholder
		// is what FAAS_PADDLE_SANDBOX=1 expects; the SDK accepts it
		// and only rejects on auth at runtime, which is fine because
		// no e2e test reaches a real Paddle call. The dedicated
		// paddle_sandbox_e2e tests under cmd/e2e/billing_paddle_sandbox
		// override these via secrets/.env.sandbox (PR-D workflow +
		// `make e2e-sandbox`) and are unaffected.
		"FAAS_PADDLE_SANDBOX=1",
		"FAAS_PADDLE_API_KEY=pdl_test_e2e_placeholder",
		"FAAS_PADDLE_WEBHOOK_SECRET=whk_test_e2e_placeholder",
	}
	if currentHarness != nil && currentHarness.RecoveryHMACKeyHex != "" {
		env = append(env, "FAAS_MFA_RECOVERY_HMAC_KEY="+currentHarness.RecoveryHMACKeyHex)
	}
	if currentHarness != nil && currentHarness.HostHMACKeyPath != "" {
		env = append(env, "FAAS_HOST_HMAC_KEY_PATH="+currentHarness.HostHMACKeyPath)
	}
	return env
}

// newRecoveryHMACKeyHex returns a fresh 64-char hex string (32 bytes
// when decoded) suitable for FAAS_MFA_RECOVERY_HMAC_KEY. Per-test
// uniqueness is the point — see the doc on Harness.RecoveryHMACKeyHex.
// On the astronomically unlikely chance that crypto/rand fails we
// Fatalf the test rather than return an empty key (which would let
// apid refuse-to-start and the harness report a misleading
// timeout-from-port-not-bound as the boot failure).
func newRecoveryHMACKeyHex(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("e2etest: crypto/rand for recovery HMAC key: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// newHostHMACKeyFile writes a fresh 32-byte file (mode 0o400) into
// the harness's per-test TmpDir and returns the path. apid's
// loadHostHMACKey expects raw 32 bytes (NOT hex-encoded) at the
// FAAS_HOST_HMAC_KEY_PATH. The 0o400 perm matches the
// production posture so the e2e path exercises the same
// 503 / refuse-to-start branch. The file is fresh per test
// (crypto/rand) so two parallel tests don't share a key, and
// so a leaked key from one test's debug output is useless for
// the next test's value_hash discriminator.
func newHostHMACKeyFile(t *testing.T, dir string) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("e2etest: crypto/rand for host HMAC key: %v", err)
	}
	path := filepath.Join(dir, "host.hmac.key")
	if err := os.WriteFile(path, b[:], 0o400); err != nil {
		t.Fatalf("e2etest: write host HMAC key file %s: %v", path, err)
	}
	return path
}

// writeScheddSignPub generates a fresh ECDSA P-256 keypair (the
// canonical cosign signer/verifier surface — see ADR-038 + the
// `faas sign-keys init` subcommand) and writes the public half into
// h.SockDir at the canonical path + 0444 mode so schedd's fail-loud
// load at cmd/schedd/main.go:232-239 doesn't reject the boot.
//
// Production deploys read /etc/faas/secrets/sign-pub.pem (the
// FAAS_SIGN_PUB env override defaults to that); the harness sets
// FAAS_SIGN_PUB to the temp-dir path so the test subprocess sees the
// test-generated pub key. Without this helper, schedd would exit
// immediately and every test that boots schedd would race the 30s
// startProc deadline (or — once started — silently skip the
// verification path the rest of the PR adds).
//
// Cleanup is automatic via h.TmpDir (t.TempDir).
func writeScheddSignPub(t *testing.T, h *Harness) string {
	t.Helper()
	_, pubPEM, err := cosign.GenerateKeyPair()
	if err != nil {
		t.Fatalf("e2etest: generate cosign keypair: %v", err)
	}
	pubPath := filepath.Join(h.SockDir, "sign-pub.pem")
	if err := os.WriteFile(pubPath, pubPEM, 0o444); err != nil {
		t.Fatalf("e2etest: write sign-pub.pem: %v", err)
	}
	return pubPath
}

// startMeterd boots meterd against the test's schedd unix socket. The
// Stripe push is intentionally disabled — STRIPE_API_KEY is left blank,
// which the meterd wire-up warns about and the stripex SDK call skips
// (issue #52 acceptance path uses an empty apiKey surface anyway).
//
// extraEnv is appended last so a test can inject FAAS_QUOTA_INTERVAL for
// the "parked within one tick" gate (60s default would make the test
// take a minute). Pass nil from the no-extras path inside Start.
//
// meterd does NOT expose a listener socket (issue #52 surface) — no
// waitTCP / waitUnix after startProc.
func startMeterd(t *testing.T, h *Harness, bin, dbURL string, extraEnv ...[]string) {
	t.Helper()
	env := append(testEnvCommon(dbURL),
		"FAAS_SCHEDD_ADDR="+h.ScheddSock,
	)
	for _, e := range extraEnv {
		env = append(env, e...)
	}
	h.procs = append(h.procs, startProc(t, bin, "meterd", env))
}

// stop SIGTERMs every daemon, waits up to 5s, then SIGKILL stragglers. Owns
// the single cmd.Wait per process — startProc must not call it (would race).
//
// Every daemon's stdout/stderr is dumped to the test log on teardown —
// including a clean exit — so a quota-not-flipping e2e failure has the
// meterd loop's last words to bisect with. The buffer is otherwise lost
// when startProc's bytes.Buffer is GC'd; surfacing it always is cheaper
// than re-running with -v on a CI flake (issue #52 PR #59 follow-up).
func (h *Harness) stop() {
	for _, p := range h.procs {
		if p.Process == nil {
			continue
		}
		_ = p.Process.Signal(syscall.SIGTERM)
	}
	for _, proc := range h.procs {
		if proc.Process == nil || proc.ProcessState != nil {
			continue
		}
		done := make(chan struct{})
		go func(p *exec.Cmd) {
			_ = p.Wait()
			close(done)
		}(proc)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = proc.Process.Kill()
			<-done
		}
		// Always dump the daemon's last words so an e2e t.Fatalf has
		// meterd's quota-tick output to reason about.
		if buf, ok := proc.Stdout.(*safeBuffer); ok {
			h.T.Logf("e2etest: %s final state=%v\n%s",
				filepath.Base(proc.Path), proc.ProcessState, buf.String())
		}
	}
}

// Stop terminates every daemon subprocess owned by this Harness
// immediately, without waiting for the test's t.Cleanup. Used by
// e2e tests that need to release shared resources (Postgres
// connections held by an apid pool, in-flight LISTEN goroutines,
// etc.) between two boot phases — see
// cmd/e2e/secrets_rotate_box_e2e_test.go::TestRekeyRunnerPg.
//
// Calls unexported stop() once. Idempotent: a second call after
// the daemons are already reaped is a no-op (the inner loop
// short-circuits on p.ProcessState != nil). The shared test pool
// (h.Pool) is NOT closed here — that's owned by pgtest.Open's
// t.Cleanup. Closing the pool mid-test would deadlock the next
// StartWithEnv call.
//
// ADR-094: the L2 fix for PR-823's TestRekeyRunnerPg flake. The
// listener-bind timeout (`127.0.0.1:<port> did not accept within
// 10s`) was caused by phase 2's apid racing phase 1's
// already-running bgBefore goroutines for the shared Postgres
// max_connections=100 budget. Calling Stop between phases
// removes phase 1 from contention so phase 2 boots cleanly.
func (h *Harness) Stop() {
	if h == nil {
		return
	}
	h.stop()
}

// buildBinaries runs `go build` for each daemon listed in whichDaeamons into
// the bin dir. The Go build cache means the second run in the same test
// process is a no-op.
//
// Uses the full module import path (not the ./cmd/<d> form) so the subprocess
// doesn't need to know the test's CWD — `go test` runs with the test's
// directory as CWD, which breaks the relative-path form.
func buildBinaries(t *testing.T, bin string) {
	t.Helper()
	modulePath := modulePath(t)
	// Tier A7 (ADR-070) PR-A: the legacy 'gatewayd' binary is gone
	// (its source moved into cmd/gatewayd-internal/). PR-B will boot
	// the split pair (gatewayd-public + gatewayd-internal) in
	// startGatewayd; for now we just build the new daemons so the
	// binary dir is consistent.
	for _, d := range []string{"apid", "schedd", "vmmd", "imaged", "gatewayd-internal", "meterd", "builderd"} {
		out := filepath.Join(bin, d)
		cmd := exec.Command("go", "build", "-o", out, modulePath+"/cmd/"+d)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("e2etest: go build %s: %v", d, err)
		}
	}
}

// modulePath derives the module path from this file's location (the package
// source is at <module>/pkg/e2etest/, so two dirs up is the module root and
// `go list -m` reports the path). Falls back to reading go.mod if go list
// fails (sandbox without network).
func modulePath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	// Last-resort: parse go.mod manually.
	data, rerr := os.ReadFile("go.mod")
	if rerr != nil {
		t.Fatalf("e2etest: cannot determine module path (go list: %v, go.mod: %v)", err, rerr)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	t.Fatal("e2etest: go.mod has no module line")
	return ""
}

// safeBuffer is a sync.Mutex-guarded *bytes.Buffer. The harness's
// cmd.Stdout is written by the daemon subprocess goroutine and read
// by the test goroutine via DumpLogs / MeterdLogs; a bare *bytes.Buffer
// is not safe for concurrent Read+Write and trips -race. *os.File (the
// real stdout) is safe; this mirrors that on the test side without
// changing the daemon. In-memory only — not used in production.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// String returns a snapshot of the buffer's contents under the lock.
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// startProc runs bin/<name> with env, returns the *exec.Cmd. stdout/stderr go
// to a buffer that stop() logs if the daemon exits unexpectedly. Note: this
// function does NOT call cmd.Wait — stop() owns that single Wait. Double-Wait
// trips the race detector.
func startProc(t *testing.T, bin, name string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(filepath.Join(bin, name))
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &safeBuffer{}
	cmd.Stderr = cmd.Stdout // share the same buffer (only one consumer: stop)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("e2etest: start %s: %v", name, err)
	}
	return cmd
}

// DumpLogs prints the captured stdout/stderr of every running daemon
// subprocess to the test log. Useful when a deploy/instance waiter
// stalls and you need the daemon's last words without waiting for the
// process to exit (the stop-time Logf only fires on non-zero exit).
// Intended for debugging — production tests don't call this.
func (h *Harness) DumpLogs(t *testing.T) {
	t.Helper()
	for _, p := range h.procs {
		if buf, ok := p.Stdout.(*safeBuffer); ok {
			s := buf.String()
			if s == "" {
				continue
			}
			t.Logf("e2etest: %s captured output:\n%s", filepath.Base(p.Path), s)
		}
	}
}

// MeterdLogs returns the captured stdout/stderr of the meterd
// subprocess as a string. Returns "" if meterd wasn't started, has
// already exited, or no procs are tracked. The buffer is shared
// with stop()/DumpLogs (cmd.Stdout == cmd.Stderr per startProc at
// line 620-621), so a concurrent test that drives more pushes may
// see additional lines appended after the call returns — the
// caller is expected to re-call or poll.
//
// Used by the §14 M7 invoice-shadow e2e (cmd/e2e/billing_invoice_shadow_test.go)
// to scrape the per-push `mb_seconds` value from the meterd log,
// which is the unified oracle across both Stripe and Paddle
// (the provider-specific dedupe tables have different shapes;
// the log line is provider-neutral). See pkg/meter/pusher.go:155.
func (h *Harness) MeterdLogs() string {
	for _, p := range h.procs {
		if filepath.Base(p.Path) != "meterd" {
			continue
		}
		if buf, ok := p.Stdout.(*safeBuffer); ok {
			return buf.String()
		}
	}
	return ""
}

// VmmdLogs returns the captured stdout/stderr of the vmmd subprocess.
// Mirrors MeterdLogs (the same shared-buffer pattern). The buffer is
// shared with stop()/DumpLogs (cmd.Stdout == cmd.Stderr per startProc
// at line 620-621), so a concurrent push may append more lines after
// this returns — the caller is expected to poll. Empty string means
// vmmd wasn't started (e.g. Harness wired with DeployWake mask) or
// has no captured output yet.
//
// Used by the §14 M6 source-deploy→wake e2e
// (cmd/e2e/source_deploy_wake_metal_test.go) to assert vmmd's
// "restore failed, falling back to cold boot" Warn at
// pkg/fcvm/manager.go:2368 fired during a vmmd-restore-fail wake
// — that log line is the load-bearing signal that the VMM-side
// fallback (vs the planner-side cold-boot rejection) actually ran.
func (h *Harness) VmmdLogs() string {
	for _, p := range h.procs {
		if filepath.Base(p.Path) != "vmmd" {
			continue
		}
		if buf, ok := p.Stdout.(*safeBuffer); ok {
			return buf.String()
		}
	}
	return ""
}

// injectSearchPath adds (or replaces) the search_path query parameter on a
// pgx DSN. The test's pool uses <schema>,public — match that so the daemon
// subprocess reads the same tables the test wrote to.
func injectSearchPath(dsn, schema string) string {
	const key = "search_path="
	if i := strings.Index(dsn, key); i >= 0 {
		// Replace existing value up to the next & or end of string.
		end := strings.IndexByte(dsn[i+len(key):], '&')
		if end < 0 {
			return dsn[:i] + key + schema
		}
		return dsn[:i] + key + schema + dsn[i+len(key)+end:]
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

// Slight race between close and the daemon re-listening, but acceptable in
// tests — the daemon retries on bind error.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("e2etest: freeTCPAddr: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitTCP dials addr every 50ms until it accepts or deadline. On timeout
// it dumps the live daemon stdout/stderr so a CI flake has the daemon's
// last words to bisect with (mirrors waitUnix's dumpProcs).
func waitTCP(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	dumpProcs(t)
	t.Fatalf("e2etest: %s did not accept within %s", addr, d)
}

// setDefaultLocalScheddTarget points the synthetic default-local
// compute_nodes row's schedd_target_url at the per-test schedd
// unix socket. Migration 00090 seeds the canonical production
// socket (/run/faas/schedd.sock), but the e2e harness boots schedd
// on a per-test temp dir. Without this UPDATE, the
// gatewayd-internal scheddrouter dials the canonical socket and
// every dispatch fails with "sched: invocation: gateway returned
// 502" (the legacy FAAS_SCHEDD_SOCKET fallback that masked this was
// removed in multi-host safety cluster PR-7 / audit F5 —
// pkg/gateway/pgbackend.go:1286 deletes the resolveSched branch
// that previously returned b.sched on transient triggers).
//
// Idempotent: the UPDATE re-applies on every Start/StartWithEnv
// call so two schedd boots in the same process (e.g. back-to-back
// subtests) both converge on the active socket.
func setDefaultLocalScheddTarget(t *testing.T, pool *pgxpool.Pool, sockPath string) {
	t.Helper()
	target := "unix://" + sockPath
	if _, err := pool.Exec(context.Background(),
		`update compute_nodes set schedd_target_url = $1 where name = 'default-local'`,
		target); err != nil {
		t.Fatalf("e2etest: set default-local schedd_target_url: %v", err)
	}
}

// waitUnix polls for a unix socket file to exist and accept. The daemon
// creates the file before binding, so file-existence is a strong signal.
func waitUnix(t *testing.T, path string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Surface the daemon's last words on a wait-timeout so a CI flake has
	// something to bisect with — cmd/e2e schedd boot is the hottest failure
	// surface and the buffer is otherwise discarded when stop() runs after
	// t.Fatalf (issue #52 PR #59 follow-up).
	dumpProcs(t)
	t.Fatalf("e2etest: %s not listening within %s", path, d)
}

// dumpProcs flushes every still-running proc's stdout/stderr buffer to the
// test log. Called from waitUnix on timeout so the failing test prints the
// daemon's last words before the harness tears down via t.Cleanup.
func dumpProcs(t *testing.T) {
	t.Helper()
	for _, p := range snapshotProcs() {
		if p == nil || p.Process == nil {
			continue
		}
		if p.ProcessState != nil {
			continue
		}
		if buf, ok := p.Stdout.(*safeBuffer); ok {
			t.Logf("e2etest: %s still running, output:\n%s", filepath.Base(p.Path), buf.String())
		}
	}
}

// envBuilderBase returns FAAS_BUILDER_BASE_PATH if set (lets the harness
// point builderd at the Lima-staged arm64 rootfs), otherwise the EX44 default.
// Mirrors cmd/imaged/main.go's envOr pattern.
func envBuilderBase(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("FAAS_BUILDER_BASE_PATH"); v != "" {
		return v
	}
	return "/srv/fc/base/builder-base.ext4"
}

// waitBuilderdListens polls the test's pgxpool for a backend session tagged
// application_name='faas-builderd'. cmd/builderd/main.go's OpenWithAppName
// sets this on every connection (including the long-lived LISTEN one), so
// seeing the tag proves the daemon is past db.Open + MigrateUp + db.Subscribe
// and is ready to receive the harness's first build_queued notification.
//
// Filtering on application_name rather than `query ILIKE '%LISTEN%build_queued%'`
// eliminates two races: (a) pg_stat_activity reports `query` as the last
// query, which on a fast-rebooted daemon can be a stale `LISTEN` from the
// previous session; (b) apid or schedd's pg_stat_activity rows never collide
// because they don't tag themselves 'faas-builderd'. Avoids the stderr-polling
// race against startProc's bytes.Buffer (which the stop() path owns
// exclusively).
func (h *Harness) waitBuilderdListens(d time.Duration) error {
	h.T.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var ready bool
		if err := h.Pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = 'faas-builderd')`,
		).Scan(&ready); err == nil && ready {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("no application_name='faas-builderd' in pg_stat_activity within %s", d)
}

// SeedAccount creates a fresh account on `plan` with one API key, returns the
// plaintext token (Bearer header). Returns the existing account on a duplicate
// email so reruns against the same schema pick up where they left off.
//
// Pass a non-empty label to disambiguate when the test needs more than one
// account on the same plan (cross-account isolation, multi-tenant tests).
// Without a label, the email is "e2e+<plan>@test.example" — one account per
// plan per run. With a label, the email is "e2e+<plan>+<label>@test.example"
// so each call produces a distinct account.
func (h *Harness) SeedAccount(ctx context.Context, plan api.Plan, label ...string) string {
	h.T.Helper()
	store := state.NewPgStore(h.Pool)
	email := "e2e+" + string(plan)
	if len(label) > 0 && label[0] != "" {
		email += "+" + label[0]
	}
	email += "@test.example"
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: email,
		Plan:  plan,
	})
	if err != nil {
		// "duplicate key" / "unique_violation" — another subtest already
		// seeded this plan+label; fetch and reuse.
		acct, lerr := store.AccountByEmail(ctx, email)
		if lerr != nil {
			h.T.Fatalf("e2etest: seed account %s (initial=%v, lookup=%v)", plan, err, lerr)
		}
		pt, hash, gerr := api.GenerateAPIKey()
		if gerr != nil {
			h.T.Fatalf("e2etest: generate API key: %v", gerr)
		}
		if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
			h.T.Logf("e2etest: store API key (already exists, ignoring): %v", err)
		}
		return pt
	}
	acct := res.Account
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		h.T.Fatalf("e2etest: generate API key: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
		h.T.Fatalf("e2etest: store API key: %v", err)
	}
	return pt
}

// SeedOrg creates a non-personal organization with the given name +
// slug, adds accountID with role, and returns the org. Used by the
// PR-4 LoadOrg e2e tests (cmd/e2e/load_org_e2e_test.go) to set up
// the multi-org matrix; PR-5+ tests will reuse it for member +
// invitation assertions.
//
// On slug collision (another subtest already used this slug) the
// helper does NOT look up the existing org — that's the caller's
// job via store.OrgBySlug. The h.Fatal on conflict keeps the
// intent obvious in failing tests.
//
// Personal org (Personal: true) is rejected here — e2e tests that
// need a personal org should call SeedAccount (which wraps
// CreateAccountWithPersonalOrg) and read the slug off the result.
func (h *Harness) SeedOrg(ctx context.Context, accountID, slug, name string, role state.OrgRole) state.Org {
	h.T.Helper()
	store := state.NewPgStore(h.Pool)
	org, err := store.CreateOrg(ctx, state.Org{
		Slug:     slug,
		Name:     name,
		Plan:     api.PlanFree,
		Personal: false,
		Status:   state.OrgStatusActive,
	})
	if err != nil {
		h.T.Fatalf("e2etest: seed org %s: %v", slug, err)
	}
	if err := store.AddOrgMember(ctx, org.ID, accountID, role, nil); err != nil {
		h.T.Fatalf("e2etest: add %s to org %s: %v", role, slug, err)
	}
	return org
}

// HTTPClient returns a client with a generous timeout. The e2e test's longest
// single request is the deploy (imaged pull → rootfs build → snapshot), which
// can take several seconds in CI; 30s leaves room.
func (h *Harness) HTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// silence unused-import when callers drop the io helpers.
var _ = io.Discard
