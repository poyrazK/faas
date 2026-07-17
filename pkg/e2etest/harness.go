// Package e2etest — harness helpers for the M5 acceptance tests in cmd/e2e.
//
// The harness boots every daemon as a real subprocess, each on its own port /
// unix socket inside a t.TempDir(), against one pgtest schema. Tests drive the
// production HTTP / gRPC / pg_notify surface — never the in-process types —
// so the integration is the same code path customers exercise.
//
// Why subprocesses (not in-process wiring):
//
//   - cmd/apid, cmd/schedd, cmd/imaged, cmd/gatewayd, cmd/vmmd are all
//     package main; Go forbids importing them as libraries, so the only way
//     to drive the real listener lifecycle is `go build` + `exec.Cmd`.
//   - This matches the EX44 / Lima deployment: every daemon is its own
//     process. If a test passes here, the wire is the same.
//
// Build-tag splits in cmd/e2e:
//
//   - quota_e2e_test.go            (no tag)        boots apid only; CI-safe.
//   - deploy_wake_metal_test.go    //go:build metal boots apid + schedd +
//                                                 imaged + vmmd + gatewayd.
//                                                 Needs /dev/kvm and root.
//
// Per-daemon configuration:
//
//   - apid        env    FAAS_APID_LISTEN=127.0.0.1:<port>
//   - gatewayd    env    FAAS_GATEWAY_LISTEN=127.0.0.1:<port>
//                          FAAS_SCHEDD_SOCKET=<tmp>/schedd.sock
//                          FAAS_APPS_DOMAIN=<test domain>
//   - imaged      env    FAAS_GUEST_INIT=<repo>/guest/init  (or empty)
//                          FAAS_APPS_ROOT=<tmp>/apps
//                          FAAS_OCI_INSECURE=1                  (test-only)
//   - schedd      toml   socket_path / vmmd_socket
//   - vmmd        toml   socket_path / kernel_path (metal tag only)
//
// FAAS_OCI_INSECURE swaps imaged's egress-guarded http.Client for a plain one
// so the fakeregistry on 127.0.0.1 is reachable. The guard denies loopback by
// design; this knob is for tests only (the WARN log makes that obvious).
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
	T         *testing.T
	Pool      *pgxpool.Pool
	TmpDir    string
	BinDir    string
	APIDURL   string
	ScheddSock string
	VMMDPath  string
	VMMDSock  string
	GatewayURL string
	ImagedTmp string // FAAS_APPS_ROOT

	// Per-daemon state. nil for a daemon not started (e.g. quota test skips
	// the metal-only daemons).
	procs []*exec.Cmd
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

	h := &Harness{T: t, Pool: pool, TmpDir: tmp, BinDir: bin, ImagedTmp: appsRoot}

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

	if which&APID != 0 {
		addr := freeTCPAddr(t)
		env := []string{
			"FAAS_APID_LISTEN=" + addr,
			"FAAS_APPS_DOMAIN=" + testDomain,
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		h.procs = append(h.procs, startProc(t, bin, "apid", env))
		h.APIDURL = "http://" + addr
		waitTCP(t, addr, 10*time.Second)
	}

	if which&Schedd != 0 {
		sockPath := filepath.Join(tmp, "schedd.sock")
		vmmdSock := filepath.Join(tmp, "vmmd.sock")
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
		env := []string{
			"FAAS_SCHEDD_CONFIG=" + cfgPath,
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		h.procs = append(h.procs, startProc(t, bin, "schedd", env))
		h.ScheddSock = sockPath
		h.VMMDSock = vmmdSock
		waitUnix(t, sockPath, 10*time.Second)
	}

	if which&VMMD != 0 {
		// Metal-only path. Caller is responsible for ensuring /dev/kvm + root.
		sockPath := h.VMMDSock
		if sockPath == "" {
			sockPath = filepath.Join(tmp, "vmmd.sock")
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
		env := []string{
			"FAAS_VMMD_CONFIG=" + cfgPath,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
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
		env := []string{
			"FAAS_GUEST_INIT=" + guestInit,
			"FAAS_APPS_ROOT=" + appsRoot,
			"FAAS_OCI_INSECURE=1",
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		h.procs = append(h.procs, startProc(t, bin, "imaged", env))
	}

	if which&Gatewayd != 0 {
		addr := freeTCPAddr(t)
		if h.ScheddSock == "" {
			h.ScheddSock = filepath.Join(tmp, "schedd.sock")
		}
		env := []string{
			"FAAS_GATEWAY_LISTEN=" + addr,
			"FAAS_SCHEDD_SOCKET=" + h.ScheddSock,
			"FAAS_APPS_DOMAIN=" + testDomain,
			"DATABASE_URL=" + dbURL,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		h.procs = append(h.procs, startProc(t, bin, "gatewayd", env))
		h.GatewayURL = "http://" + addr
		waitTCP(t, addr, 10*time.Second)
	}

	t.Cleanup(h.stop)
	return h
}

// Which flags select which daemons to boot. Bitmask so a test can ask for
// just apid (quota) or all five (metal).
type Which int

const (
	APID Which = 1 << iota
	Schedd
	VMMD
	Imaged
	Gatewayd
)

// All is the full set for the metal e2e test.
const All = APID | Schedd | VMMD | Imaged | Gatewayd

const testDomain = "apps.test.example"

// stop SIGTERMs every daemon, waits up to 5s, then SIGKILL stragglers. Owns
// the single cmd.Wait per process — startProc must not call it (would race).
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
		// Surface unexpected exits with the daemon's last log lines.
		if proc.ProcessState != nil && !proc.ProcessState.Success() {
			if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
				h.T.Logf("e2etest: %s exited %v\n%s", filepath.Base(proc.Path), proc.ProcessState, buf.String())
			}
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
	for _, d := range []string{"apid", "schedd", "vmmd", "imaged", "gatewayd"} {
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
	t.Fatalf("e2etest: %s not listening within %s", path, d)
}

// SeedAccount creates a fresh account on `plan` with one API key, returns the
// plaintext token (Bearer header). Returns the existing account on a duplicate
// email so reruns against the same schema pick up where they left off.
func (h *Harness) SeedAccount(ctx context.Context, plan api.Plan) string {
	h.T.Helper()
	store := state.NewPgStore(h.Pool)
	email := "e2e+" + string(plan) + "@test.example"
	acct, err := store.CreateAccount(ctx, email, plan)
	if err != nil {
		// "duplicate key" / "unique_violation" — another subtest already
		// seeded this plan; fetch and reuse.
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