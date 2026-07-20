// Package e2etest — harness helpers for the M5 acceptance tests in cmd/e2e.
//
// The harness boots every daemon as a real subprocess, each on its own port /
// unix socket inside a t.TempDir(), against one pgtest schema. Tests drive the
// production HTTP / gRPC / pg_notify surface — never the in-process types —
// so the integration is the same code path customers exercise.
//
// Why subprocesses (not in-process wiring):
//
//   - cmd/apid, cmd/schedd, cmd/imaged, cmd/gatewayd, cmd/vmmd, cmd/meterd
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
//     imaged + vmmd + gatewayd.
//     Needs /dev/kvm and root.
//
// Per-daemon configuration:
//
//   - apid        env    FAAS_APID_LISTEN=127.0.0.1:<port>
//   - gatewayd    env    FAAS_GATEWAY_LISTEN=127.0.0.1:<port>
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
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Harness owns one booted set of daemons + the shared PG pool. Stop() is
// registered via t.Cleanup so an assertion failure still tears everything down.
//
// Fields are exported for test consumption: H.APIDURL, H.GatewayURL, H.Pool.
type Harness struct {
	T           *testing.T
	Pool        *pgxpool.Pool
	TmpDir      string
	BinDir      string
	SockDir     string // short-path unix-socket directory (see Start comment)
	APIDURL     string
	ScheddSock  string
	VMMDPath    string
	VMMDSock    string
	GatewayURL  string
	ImagedTmp   string // FAAS_APPS_ROOT
	BuilderdCfg string // FAAS_BUILDERD_CONFIG path (issue #57 M6 e2e)

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

	h := &Harness{T: t, Pool: pool, TmpDir: tmp, BinDir: bin, ImagedTmp: appsRoot, SockDir: sockDir}
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
	// 12 = migrations/00012_account_stripe_subscription_item.sql (current head).
	pgtest.WaitForMigration(t, pool, 12, 10*time.Second)

	if which&APID != 0 {
		startAPID(t, h, bin, dbURL)
	}

	if which&Schedd != 0 {
		sockPath := filepath.Join(h.SockDir, "schedd.sock")
		vmmdSock := filepath.Join(h.SockDir, "vmmd.sock")
		// schedd needs to dial vmmd; on the metal tag vmmd is started below
		// and we use the same path. On tests that skip vmmd, schedd still
		// starts — it just warns on wake. Wire both paths so schedd config
		// is consistent regardless of order.
		cfgPath := filepath.Join(tmp, "schedd.toml")
		cfg := fmt.Sprintf(
			`socket_path = %q
owner_user = %q
vmmd_socket = %q
`,
			sockPath, "root", vmmdSock,
		)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
			t.Fatalf("e2etest: write schedd.toml: %v", err)
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_SCHEDD_CONFIG="+cfgPath,
		)
		h.procs = append(h.procs, startProc(t, bin, "schedd", env))
		h.ScheddSock = sockPath
		h.VMMDSock = vmmdSock
		// 30s tolerates schedd's first-boot db.MigrateUp on a fresh
		// schema — observed 16s on CI's postgres15 service for 12
		// migrations. The metal path reuses the same socket so this
		// ceiling also covers the post-migration cold start.
		waitUnix(t, sockPath, 30*time.Second)
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
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		)
		// Optional builder-base override (Lima / CI without ghcr creds). When
		// FAAS_TEST_BUILDER_BASE_REF is set, imaged pulls the base from there
		// instead of the production ghcr.io/onebox-faas/builder-base:latest
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
			env = append(env, "FAAS_DEPLOY_BASE_REF="+dbr)
		}
		h.procs = append(h.procs, startProc(t, bin, "imaged", env))
	}

	if which&Gatewayd != 0 {
		addr := freeTCPAddr(t)
		if h.ScheddSock == "" {
			h.ScheddSock = filepath.Join(h.SockDir, "schedd.sock")
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_GATEWAY_LISTEN="+addr,
			"FAAS_SCHEDD_SOCKET="+h.ScheddSock,
			"FAAS_APPS_DOMAIN="+testDomain,
		)
		h.procs = append(h.procs, startProc(t, bin, "gatewayd", env))
		h.GatewayURL = "http://" + addr
		waitTCP(t, addr, 10*time.Second)
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
	h := &Harness{T: t, Pool: pool, TmpDir: tmp, BinDir: bin, ImagedTmp: appsRoot, SockDir: sockDir}
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
	// cmd-e2e-schedd-migration-race memory).
	pgtest.WaitForMigration(t, pool, 12, 10*time.Second)

	if which&APID != 0 {
		addr := freeTCPAddr(t)
		env := append(testEnvCommon(dbURL),
			"FAAS_APID_LISTEN="+addr,
			"FAAS_APPS_DOMAIN="+testDomain,
		)
		env = append(env, extraEnv...)
		h.procs = append(h.procs, startProc(t, bin, "apid", env))
		h.APIDURL = "http://" + addr
		waitTCP(t, addr, 10*time.Second)
	}
	if which&Schedd != 0 {
		sockPath := filepath.Join(h.SockDir, "schedd.sock")
		vmmdSock := filepath.Join(h.SockDir, "vmmd.sock")
		cfgPath := filepath.Join(tmp, "schedd.toml")
		cfg := fmt.Sprintf(
			`socket_path = %q
owner_user = %q
vmmd_socket = %q
`,
			sockPath, "root", vmmdSock,
		)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
			t.Fatalf("e2etest: write schedd.toml: %v", err)
		}
		env := append(testEnvCommon(dbURL),
			"FAAS_SCHEDD_CONFIG="+cfgPath,
		)
		env = append(env, extraEnv...)
		h.procs = append(h.procs, startProc(t, bin, "schedd", env))
		h.ScheddSock = sockPath
		h.VMMDSock = vmmdSock
		// 30s tolerates schedd's first-boot db.MigrateUp (same
		// rationale as the Start path above).
		waitUnix(t, sockPath, 30*time.Second)
	}
	if which&Meterd != 0 {
		startMeterd(t, h, bin, dbURL, extraEnv)
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
	env := append(testEnvCommon(dbURL),
		"FAAS_APID_LISTEN="+addr,
		"FAAS_APPS_DOMAIN="+testDomain,
	)
	h.procs = append(h.procs, startProc(t, bin, "apid", env))
	h.APIDURL = "http://" + addr
	waitTCP(t, addr, 10*time.Second)
}

// testEnvCommon returns the env every daemon gets in the harness:
//   - DATABASE_URL  (per-test schema via search_path injection in the
//     caller; the daemon's pool therefore targets the same schema the
//     test seeded rows in)
//   - FAAS_SKIP_SOCKET_GROUP=1 — see package doc comment. Without it,
//     the daemon's wire.ListenOrRecreateByName errors on a host without
//     the `faas` group, which is every CI runner and dev Mac. Production
//     deploys have the group; the ansible role creates it at bootstrap.
//   - PATH / HOME inherited so go-built daemons can `exec.LookPath`
//     helpers (notably firecracker, which schedd warns about but does
//     not require for the meterd quota gate).
func testEnvCommon(dbURL string) []string {
	return []string{
		"DATABASE_URL=" + dbURL,
		"FAAS_SKIP_SOCKET_GROUP=1",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
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
		if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
			h.T.Logf("e2etest: %s final state=%v\n%s",
				filepath.Base(proc.Path), proc.ProcessState, buf.String())
		}
	}
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
	for _, d := range []string{"apid", "schedd", "vmmd", "imaged", "gatewayd", "meterd", "builderd"} {
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

// startProc runs bin/<name> with env, returns the *exec.Cmd. stdout/stderr go
// to a buffer that stop() logs if the daemon exits unexpectedly. Note: this
// function does NOT call cmd.Wait — stop() owns that single Wait. Double-Wait
// trips the race detector.
func startProc(t *testing.T, bin, name string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(filepath.Join(bin, name))
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &bytes.Buffer{}
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
		if buf, ok := p.Stdout.(*bytes.Buffer); ok {
			s := buf.String()
			if s == "" {
				continue
			}
			t.Logf("e2etest: %s captured output:\n%s", filepath.Base(p.Path), s)
		}
	}
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

// waitTCP dials addr every 50ms until it accepts or deadline.
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
	t.Fatalf("e2etest: %s did not accept within %s", addr, d)
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
		if buf, ok := p.Stdout.(*bytes.Buffer); ok {
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
	acct, err := store.CreateAccount(ctx, email, plan)
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
		if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e"); err != nil {
			h.T.Logf("e2etest: store API key (already exists, ignoring): %v", err)
		}
		return pt
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		h.T.Fatalf("e2etest: generate API key: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e"); err != nil {
		h.T.Fatalf("e2etest: store API key: %v", err)
	}
	return pt
}

// HTTPClient returns a client with a generous timeout. The e2e test's longest
// single request is the deploy (imaged pull → rootfs build → snapshot), which
// can take several seconds in CI; 30s leaves room.
func (h *Harness) HTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// silence unused-import when callers drop the io helpers.
var _ = io.Discard
