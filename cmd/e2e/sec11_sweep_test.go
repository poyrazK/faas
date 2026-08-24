// sec11_sweep_test.go — M8 §11 security-hardening e2e sweep.
//
// Spec §11 ("security hardening checklist") is ship-blocking (§14 M8 row:
// "security checklist signed off item-by-item"). Each test below pins one
// bullet of that checklist at the e2e / cross-process layer that the
// package-level unit tests in pkg/fcvm, pkg/netns, pkg/secretbox,
// pkg/middleware, pkg/api cannot reach on their own.
//
// Linux-only host checks (//go:build linux) live in
// sec11_host_linux_test.go so this file compiles on macOS dev and on
// ubuntu-latest CI alike.
//
// Out of scope (separate PRs, per the plan):
//   - live nft list ruleset (CAP_NET_ADMIN)
//   - auditd execve rules (auditd daemon)
//   - jailer seccomp assertion (KVM + seccomp-tools)
//   - FC-upgrade drill (second firecracker binary)
//   - crypto-mining heuristic detector (not implemented)
//   - V6 entropy reseed e2e cross-process (metal-only)
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// openSchemaPG opens pgtest, runs migrations to the current head, and
// returns a per-test pool. Mirrors the opening dance in
// quota_e2e_test.go / secrets_e2e_test.go.
func openSchemaPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open skipped (no DATABASE_URL)")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	return pool
}

// freeTCPAddr asks the kernel for a free localhost port. We don't bind
// to it — apid does — but the kernel guarantees no other process will
// get it in the race window before apid's listen.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeTCPAddr: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// startProc starts a daemon subprocess with the given env, capturing
// stdout/stderr into a buffer for later inspection. Caller is
// responsible for killing + reaping.
func startProc(t *testing.T, bin string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = env
	// syncBuffer wraps bytes.Buffer with a mutex so concurrent
	// writes from vmmd's PeriodicUpdates / UpdateEgressAllowlist
	// goroutines don't trip `-race` when a later assertion reads
	// the same buffer. procBuffer drains both streams into the
	// same buffer at reap time.
	var buf syncBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("startProc(%s): %v", bin, err)
	}
	return cmd
}

// syncBuffer is a mutex-guarded io.Writer used for capturing
// subprocess stdout/stderr in startProc. Mirrors pkg/e2etest's
// SafeBuffer; kept local so this file doesn't need a new import
// path. The export shape (just io.Writer + String()) is enough
// for procBuffer's drain semantics.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitTCP polls 127.0.0.1:addr until accept succeeds or deadline.
func waitTCP(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("waitTCP: %s not listening within %s", addr, d)
}

// TestMain builds the apid + vmmd binaries into a package-level tmpdir
// before any test runs, so the per-test helpers don't pay the `go
// build` cost per test. Each test still gets its own daemon subprocess
// (each needs its own /etc/faas/secrets/* fixture and its own listen
// addr) — only the BINARY is shared, not the process.
//
// vmmd is built alongside apid because the M8 §11 host-key subtest
// (TestSec11_HostKey0400_Required::rejects_insecure_private_perms) needs
// to boot vmmd with a deliberately chmod'd-loose host.age and assert
// fail-fast on ErrHostKeyInsecurePerms — that fail-fast happens
// *before* any KVM/listener binding, so the test is CI-safe (no
// /dev/kvm needed, no root needed).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "faas-sec11-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sec11_test: mkdir tmp: %v\n", err)
		os.Exit(2)
	}

	// PR-B / ADR-121: the e2e harness drives PgStore directly
	// (state.NewPgStore(pool)) inside several *_test.go files
	// — TestRollbackSpecific_E2E and friends — to set up
	// fixtures the apid subprocess test paths can't easily
	// express. Each PgStore.MarkDeploymentLive in this process
	// therefore invokes the registered OpenAPICaptureFn; without
	// a registered impl, the default noop returns a zero
	// snapshot and the UPSERT validation rejects it with
	// `empty deployment_id` (the regression that surfaced on
	// PR-B cycle-fix R9). Wire the same impl cmd/apid uses at
	// startup so the e2e process has the real projection in
	// place before any test runs.
	openapidiff.RegisterStateCapture()

	// The test binary's cwd is the package directory; resolve to the
	// module root so `go build ./cmd/{apid,vmmd}` finds the packages.
	wd, _ := os.Getwd()
	root := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			fmt.Fprintf(os.Stderr, "sec11_test: cannot find module root from %s\n", wd)
			os.Exit(2)
		}
		root = parent
	}

	build := func(pkg, out string) {
		var buf bytes.Buffer
		c := exec.Command("go", "build", "-o", out, "./"+pkg)
		c.Dir = root
		c.Stdout = &buf
		c.Stderr = &buf
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "sec11_test: go build %s: %v\n%s", pkg, err, buf.String())
			os.Exit(2)
		}
	}

	apidBinary = filepath.Join(dir, "apid")
	build("cmd/apid", apidBinary)

	vmmdBinary = filepath.Join(dir, "vmmd")
	build("cmd/vmmd", vmmdBinary)

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// apidBinary is set once in TestMain; every test uses this path.
var apidBinary string

// vmmdBinary is set once in TestMain; the host-key private-perm
// subtest uses this path. vmmd's fail-fast on insecure host.age
// perm is reached before any KVM/listener/root need, so a CI runner
// without /dev/kvm can still exercise the assertion.
var vmmdBinary string

// envForAPID returns the base env slice every apid subprocess needs,
// WITHOUT FAAS_APID_LISTEN (startAPIDWithEnv / startAPIDAndExpectFail
// allocate the listen addr inside and append it last). Same shape as
// pkg/e2etest.testEnvCommon (harness.go:498) minus the listen var.
//
// The FAAS_MFA_RECOVERY_HMAC_KEY entry is generated fresh per test
// (matches the testEnvCommon wiring in pkg/e2etest/harness.go) so the
// apid refuse-to-start policy in cmd/apid/main.go:loadOrGenerateRecoveryHMACKey
// has a real key to load. The audit-hmac loader still falls back to
// a zero-key Warn — that path was never gated.
func envForAPID(t *testing.T, dbURL string, extra ...string) []string {
	t.Helper()
	hostHMACPath := testHostHMACKeyFile(t)
	env := []string{
		"DATABASE_URL=" + dbURL,
		"FAAS_SKIP_SOCKET_GROUP=1",      // harness convention; see harness.go:498
		"FAAS_APP_ERRORS_ENABLED=false", // harness convention; see pkg/e2etest/harness.go:804 — ADR-096 / PR-B default-on kill-switch probes `faas-apid` unix user (config.go:144-149) which doesn't exist in the CI runner, so the gRPC listener never boots. Production deploys run as `faas-apid` via systemd and remain default-on; reader-path handlers (cmd/apid/handlers_app_errors.go) read from the SQL store regardless of the listener state.
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"FAAS_APPS_DOMAIN=apps.test.example",
		"FAAS_MFA_RECOVERY_HMAC_KEY=" + testRecoveryHMACKeyHex(t),
		// ADR-117 PR-C: apid refuses to start without the per-host
		// HMAC key (loadHostHMACKey in cmd/apid/main.go). Mirror the
		// audit + recovery key posture: a fresh 32-byte key in
		// t.TempDir() with mode 0400. The key is private to the
		// test process — never logged, never re-used across tests.
		"FAAS_HOST_HMAC_KEY_PATH=" + hostHMACPath,
		// PR #962 CRIT-2 — paddle.NewProvider rejects empty apiKey. Mirror
		// of pkg/e2etest/harness.go:testEnvCommon so this file's parallel
		// apid boot also gets the placeholder keys. The pdl_… shape with
		// FAAS_PADDLE_SANDBOX=1 makes the SDK accept + only reject on auth
		// at runtime, which sec11 tests never reach.
		"FAAS_PADDLE_SANDBOX=1",
		"FAAS_PADDLE_API_KEY=pdl_test_sec11_placeholder",
		"FAAS_PADDLE_WEBHOOK_SECRET=whk_test_sec11_placeholder",
	}
	env = append(env, extra...)
	return env
}

// testHostHMACKeyFile writes a fresh 32-byte raw HMAC key into a
// t.TempDir() file with mode 0400 (the production posture for
// /etc/faas/secrets/host.hmac.key) and returns the path. The
// per-test uniqueness is intentional — the value_hash discriminator
// is keyed on this file's bytes, so re-using across tests would
// leak state.
func testHostHMACKeyFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "host.hmac.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("e2e: crypto/rand for host HMAC key: %v", err)
	}
	if err := writeWithPerm(t, path, key, 0o400); err != nil {
		t.Fatalf("write host.hmac.key: %v", err)
	}
	return path
}

// testRecoveryHMACKeyHex returns a fresh 32-byte hex string for the
// FAAS_MFA_RECOVERY_HMAC_KEY env var. Cached per (t) via t.Cleanup so
// a single test that boots apid multiple times reuses the same key
// (avoids surprising the harness with rotating keys across reboots
// of the same apid in the test body).
func testRecoveryHMACKeyHex(t *testing.T) string {
	t.Helper()
	if v, ok := tRecoverHMAC.Load(t); ok {
		return v.(string)
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("e2e: crypto/rand for recovery HMAC: %v", err)
	}
	s := hex.EncodeToString(b[:])
	tRecoverHMAC.Store(t, s)
	return s
}

// tRecoverHMAC keys per-testing.T to a fresh 32-byte hex string for
// FAAS_MFA_RECOVERY_HMAC_KEY. Per-test uniqueness prevents one
// test's debug-log key from being useful for the next test's
// recovery codes; the test-private cache lets one test that boots
// apid multiple times keep a stable key across those reboots (the
// apid recovery-hmac key is supposed to be stable for the
// lifetime of the daemon's stored mfa_recovery_codes_hash rows).
var tRecoverHMAC sync.Map // *testing.T -> string

// startAPIDWithEnv boots apid with extra env and registers a t.Cleanup
// that SIGTERMs and waits up to 5s before SIGKILL. Returns the listen
// address and the process (the buffer is logged on Cleanup so a CI
// failure has apid's last words). The listen address is allocated
// inside (matching pkg/e2etest harness pattern at harness.go:475-484)
// and threaded into FAAS_APID_LISTEN.
func startAPIDWithEnv(t *testing.T, extraEnv ...string) (string, *exec.Cmd) {
	t.Helper()
	addr := freeTCPAddr(t)
	env := append(extraEnv, "FAAS_APID_LISTEN="+addr)
	proc := startProc(t, apidBinary, env)
	waitTCP(t, addr, 10*time.Second)
	t.Cleanup(func() {
		if proc.Process == nil {
			return
		}
		_ = proc.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = proc.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = proc.Process.Kill()
			<-done
		}
	})
	return addr, proc
}

// startAPIDAndExpectFail boots apid (NO t.Cleanup) and returns the
// captured stdout/stderr plus the exit error. The semantics:
//   - returned error == *exec.ExitError with status != 0 ⇒ apid
//     exited non-zero on its own (the success case for a "must
//     fail-fast" test);
//   - returned error wraps "fail-fast missing" ⇒ apid did NOT exit
//     within `expectFailWithin` and had to be SIGKILLed — fail-fast
//     is broken;
//   - returned error == nil ⇒ apid exited with status 0 — never
//     expected by the negative host-key subtest.
//
// Caller is responsible for inspecting both the error and the
// captured output. Used by the negative host-key subtest where
// e2etest.StartWithEnv would t.Fatalf on the boot failure and mask
// the assertion.
func startAPIDAndExpectFail(t *testing.T, env []string, expectFailWithin time.Duration) (string, error) {
	t.Helper()
	addr := freeTCPAddr(t)
	fullEnv := append(env, "FAAS_APID_LISTEN="+addr)
	proc := startProc(t, apidBinary, fullEnv)
	doneCh := make(chan error, 1)
	go func() { doneCh <- proc.Wait() }()
	select {
	case err := <-doneCh:
		_ = proc.Wait()
		buf := procBuffer(proc)
		return buf, err
	case <-time.After(expectFailWithin):
		_ = proc.Process.Kill()
		<-doneCh
		buf := procBuffer(proc)
		return buf, errors.New("apid did not exit within deadline — fail-fast missing")
	}
}

// procBuffer returns the captured stdout/stderr if startProc
// attached one, else empty string. Centralizing this avoids a
// 5-line null-ok dance every call site had.
func procBuffer(proc *exec.Cmd) string {
	// startProc now uses *syncBuffer (mutex-guarded io.Writer; see
	// the comment there about -race safety). The earlier *bytes.Buffer
	// type assertion silently returned "" after the wrap — every
	// "vmmd output did not mention ..." failure looked like an empty
	// output, hiding the actual error message in the buffer. Mirror
	// the shape: if it's a *syncBuffer, call its String() method;
	// fall back to *bytes.Buffer for older test harnesses that
	// haven't migrated yet (none today, but the assertion keeps
	// the seam future-safe).
	if buf, ok := proc.Stdout.(*syncBuffer); ok {
		return buf.String()
	}
	if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
		return buf.String()
	}
	return ""
}

// startVMMDAndExpectFail boots vmmd (no listener wait — vmmd's
// fail-fast on insecure host.age perm exits BEFORE the unix-socket
// bind at line 244 of cmd/vmmd/main.go) and returns the captured
// stdout/stderr + the exit error.
//
// The semantics mirror startAPIDAndExpectFail:
//   - err != nil ⇒ vmmd exited non-zero (the success case for
//     a "must fail-fast" test);
//   - err == nil ⇒ vmmd did NOT exit within the deadline
//     and had to be SIGKILLed — fail-fast is broken.
//
// Used by TestSec11_HostKey0400_Required::rejects_insecure_private_perms.
func startVMMDAndExpectFail(t *testing.T, env []string, expectFailWithin time.Duration) (string, error) {
	t.Helper()
	proc := startProc(t, vmmdBinary, env)
	doneCh := make(chan error, 1)
	go func() { doneCh <- proc.Wait() }()
	select {
	case err := <-doneCh:
		_ = proc.Wait()
		return procBuffer(proc), err
	case <-time.After(expectFailWithin):
		_ = proc.Process.Kill()
		<-doneCh
		return procBuffer(proc), errors.New("vmmd did not exit within deadline — fail-fast missing")
	}
}

// --- TestSec11_AuthLimitPerIP_CrossProcess ------------------------------
//
// §11 "rate limit auth failures (10/min/IP)" — pinned at the
// cross-process layer. The unit test in
// cmd/apid/server_authlimit_test.go asserts the in-process middleware
// buckets 11th attempts; this test boots a real apid subprocess and
// makes 11 HTTP requests with a bogus bearer, expecting the 11th to be
// blocked with 429. Mirrors the memory "middleware-AuthLimit shared
// bucket" — fresh AuthLimit(cfg) per route silently violates spec; we
// want to pin that one bucket serves every /v1/* route.

func TestSec11_AuthLimitPerIP_CrossProcess(t *testing.T) {
	pool := openSchemaPG(t)
	addr, _ := startAPIDWithEnv(t, envForAPID(t, poolDSN(pool))...)
	client := &http.Client{Timeout: 10 * time.Second}

	// Phase 1: same IP, 10× bogus bearer — all 401. 11th — 429.
	//
	// The X-Forwarded-For header forces apid's middleware to key on the
	// supplied client IP rather than the loopback peer; the bucket is
	// per-IP in memory, so two distinct XFF values must NOT share a
	// counter (per the §11 "10/min/IP" wording).
	const sameIP = "198.51.100.7"
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/apps", nil)
		req.Header.Set("Authorization", "Bearer fp_live_bogus_"+strconv.Itoa(i))
		req.Header.Set("X-Forwarded-For", sameIP)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d want 401", i+1, resp.StatusCode)
		}
	}
	// 11th must be 429 (auth-limited).
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer fp_live_bogus_11")
	req.Header.Set("X-Forwarded-For", sameIP)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("attempt 11: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("attempt 11: status=%d want 429 (auth-limited) — body=%s", resp.StatusCode, body)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		resp.Body.Close()
		t.Errorf("attempt 11: missing Retry-After header on 429")
	}
	resp.Body.Close()

	// Phase 2 (dropped): the older draft of this test asserted a
	// second X-Forwarded-For must NOT share a bucket with the first.
	// apid's middleware.AuhtLimit uses defaultClientIP, which reads
	// net.SplitHostPort(r.RemoteAddr) — NOT X-Forwarded-For — so the
	// test's assumption was incorrect; in this in-process harness the
	// TCP peer is always 127.0.0.1 regardless of XFF. The per-IP
	// isolation invariant (no shared bucket across routes / sources)
	// is pinned in pkg/middleware/middleware_test.go::TestAuthLimit,
	// not here. We retain phase 1 (same-IP 11th → 429) which is the
	// §11 cross-process bullet this PR was opened for.
	//
	// A future PR that wires apid to a trust=X-Forwarded-For
	// upstream (gatewayd-issued XFF when present) can re-add a
	// phase-2 assertion that uses two distinct TCP peers — the
	// currently cheapest shape is two net.Dial calls into a proxy
	// that rewrites source.
}

// --- TestSec11_ApiKeyHashedAtRest ----------------------------------------
//
// §11 "API keys hashed". The pkg/api/apikey_test.go unit test pins the
// hash function; this test pins the row shape: the api_keys row holds
// sha256(bearer) and never contains the plaintext.

func TestSec11_ApiKeyHashedAtRest(t *testing.T) {
	pool := openSchemaPG(t)
	// startAPIDWithEnv ensures the apid subprocess is alive so the
	// read-side test (no listener needed) inherits a working schema.
	addr, _ := startAPIDWithEnv(t, envForAPID(t, poolDSN(pool))...)
	_ = addr

	// Seed an account via the harness; we don't need the HTTP loop
	// here, but the bearer is the round-trip target.
	h := &e2etest.Harness{T: t, Pool: pool}
	bearer := h.SeedAccount(context.Background(), api.PlanHobby, "sec11")
	wantHash := api.HashAPIKey(bearer)

	var gotHash []byte
	err := pool.QueryRow(context.Background(),
		`SELECT key_sha256 FROM api_keys
		 WHERE account_id = (
		   SELECT id FROM accounts WHERE email = 'e2e+hobby+sec11@test.example'
		 ) LIMIT 1`,
	).Scan(&gotHash)
	if err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	if !bytes.Equal(gotHash, wantHash) {
		t.Errorf("key_sha256 mismatch: got %x want %x", gotHash, wantHash)
	}
	if strings.Contains(string(gotHash), bearer) {
		t.Errorf("plaintext bearer %q found inside key_sha256 %x — NOT hashed", bearer, gotHash)
	}
	if strings.Contains(string(gotHash), "fp_live_") {
		t.Errorf("plaintext key prefix %q found inside key_sha256 %x", "fp_live_", gotHash)
	}
}

// poolDSN returns the DSN apid should connect with. It mirrors
// pkg/e2etest.startAPID (harness.go:148-154): take $DATABASE_URL when
// set, otherwise fall back to the local unix-socket default, then
// inject search_path=<schema>,public so the daemon subprocess writes
// into the same schema the test seeded rows in. Without the injection
// the daemon's pool targets `public` and every "where is the seeded
// account?" lookup in the test reads from the empty schema.
func poolDSN(pool *pgxpool.Pool) string {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	if schema := pgtest.SchemaOf(pool); schema != "" {
		const key = "search_path="
		if i := strings.Index(dbURL, key); i >= 0 {
			end := strings.IndexByte(dbURL[i+len(key):], '&')
			if end < 0 {
				return dbURL[:i] + key + schema
			}
			return dbURL[:i] + key + schema + dbURL[i+len(key)+end:]
		}
		sep := "?"
		if strings.Contains(dbURL, "?") {
			sep = "&"
		}
		return dbURL + sep + key + schema
	}
	return dbURL
}

// cfgHost extracts the host portion from a pgx DSN. Used by
// TestSec11_UnixSocketOnlyDSN to skip when CI forces a TCP target
// (which we can detect by either a query `host=...` or a URL
// authority). Returns "" for unix-socket-only DSNs whose pgx form
// is `postgres:///faas` (no authority) or `host=/run/postgresql`
// (a path, not a hostname); callers use "" to mean "skip the
// TCP-detection branch and assert unix-socket only".
func cfgHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err == nil && u.Host != "" {
		// Authority (userinfo@host:port) is sufficient evidence of TCP.
		return strings.SplitN(u.Host, ":", 2)[0]
	}
	for _, kv := range strings.FieldsFunc(dsn, func(r rune) bool { return r == '&' || r == ' ' }) {
		if strings.HasPrefix(kv, "host=") {
			host := strings.TrimPrefix(kv, "host=")
			// pgx unix-socket form is host=/run/postgresql, host=., or
			// host=/var/run/postgresql. Anything starting with "/" is a
			// file path. A bare "." or ".." is also unix-socket by pgx
			// convention; the local test harness is fine with that.
			if strings.HasPrefix(host, "/") || host == "." || host == ".." {
				return ""
			}
			return host
		}
	}
	return ""
}

// --- TestSec11_UnixSocketOnlyDSN -----------------------------------------
//
// §11 "Postgres on unix socket only". After boot we query
// pg_stat_activity for any session of the current user — every row
// must have client_addr IS NULL (i.e. unix-socket peer auth). A future
// refactor that defaults to localhost would fail here.
//
// This test is intentionally skipped when DATABASE_URL points to a
// TCP host (e.g. the github-actions postgres service container at
// 172.18.0.1): the §11 requirement is a production host-baseline
// choice, not something the CI runner can provide. Tests on the EX44
// (or a CI box provisioned with a unix-socket pgbouncer) will run the
// assertion; everywhere else we skip.

func TestSec11_UnixSocketOnlyDSN(t *testing.T) {
	pool := openSchemaPG(t)
	// Skip when the test target is a TCP-backed Postgres: there's no
	// value in asserting "no TCP from apid" when apid has no other
	// choice. The skip is *intentional* — see PR #153 review note
	// (round-3) for the four failure cases this resolves.
	if host := cfgHost(poolDSN(pool)); host != "" {
		t.Skipf("DATABASE_URL host=%q is TCP; unix-socket only is a production-host baseline (EX44)", host)
	}
	addr, _ := startAPIDWithEnv(t, envForAPID(t, poolDSN(pool))...)
	_ = addr

	// Poll pg_stat_activity until apid's pool has registered a session.
	// This avoids the 200ms-sleep window where apid is still in
	// db.Ping and the "no rows" branch would mis-fail the test.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND usename = current_user`,
		).Scan(&n); err == nil && n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Cast client_addr to text so pgx returns a string instead of an
	// inet type that pgx v5 reports in binary format. We only care
	// whether the value is NULL (unix socket) or non-NULL (TCP).
	rows, err := pool.Query(context.Background(),
		`SELECT client_addr::text FROM pg_stat_activity
		 WHERE datname = current_database() AND usename = current_user`)
	if err != nil {
		t.Fatalf("query pg_stat_activity: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var addr *string
		if err := rows.Scan(&addr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if addr != nil {
			t.Errorf("session client_addr = %q — expected NULL (unix socket only)", *addr)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if count == 0 {
		t.Fatal("no sessions in pg_stat_activity — apid never connected?")
	}
}

// --- TestSec11_HostKey0400_Required --------------------------------------
//
// §11 "secrets /etc/faas/secrets/host.age 0400". The package-level
// hostkey_test.go pins the mode-bit check; this test boots a real apid
// twice — once with the allowed-mode file (0444), once with a
// too-permissive mode (0664) that LoadRecipient must reject.
//
// apid's startup fail-fast path (cmd/apid/main.go run → LoadRecipient
// → return error) must surface a non-zero exit and the
// ErrRecipientInsecurePerms sentinel substring.

func TestSec11_HostKey0400_Required(t *testing.T) {
	pool := openSchemaPG(t)

	// Note: t.Run("rejects_insecure_private_perms", ...) below boots
	// vmmd (not apid) because vmmd is the *only* component on the box
	// that reads the private host.age — apid has only the public half
	// (host.age.pub, covered by accepts_allowed_perms /
	// rejects_insecure_perms below). The private-side check is the
	// §11 ship-blocker that closes the "an unprivileged user copied
	// host.age and can now unseal every customer's env vars" gap.

	t.Run("accepts_allowed_perms", func(t *testing.T) {
		dir := t.TempDir()
		pub := filepath.Join(dir, "host.age.pub")
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("GenerateX25519Identity: %v", err)
		}
		if err := writeWithPerm(t, pub, []byte(id.Recipient().String()), 0o444); err != nil {
			t.Fatalf("write pub: %v", err)
		}
		addr, _ := startAPIDWithEnv(t, append(envForAPID(t, poolDSN(pool)),
			"FAAS_HOST_AGE_RECIPIENT_PATH="+pub)...)
		// /healthz is a cheap loopback probe — no auth, no DB work.
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatalf("healthz: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthz: status=%d want 200", resp.StatusCode)
		}
	})

	t.Run("rejects_insecure_perms", func(t *testing.T) {
		dir := t.TempDir()
		pub := filepath.Join(dir, "host.age.pub")
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("GenerateX25519Identity: %v", err)
		}
		// 0664 — group write bit is set → LoadRecipient rejects (its
		// allowedPerm mask is 0o644 = r/w for owner, r for group/other).
		// Use writeWithPerm, not os.WriteFile, because the GH runner
		// (umask 0002) lets the requested bits land on disk as written
		// while macOS dev (umask 0022) strips g/o-write and the test
		// would pass-vacuous. writeWithPerm Opens with O_CREATE then
		// Chmods to the exact requested mode.
		if err := writeWithPerm(t, pub, []byte(id.Recipient().String()), 0o664); err != nil {
			t.Fatalf("write pub: %v", err)
		}
		// Sanity-check the file landed on disk with the requested bits
		// before we boot apid — if the umask stripped group-write we
		// want to fail loudly with a useful message rather than mask
		// the bug.
		fi, lerr := os.Stat(pub)
		if lerr != nil {
			t.Fatalf("stat pub: %v", lerr)
		}
		if fi.Mode().Perm()&0o020 == 0 {
			t.Fatalf("test setup failed: file is %04o, writeWithPerm did not preserve group-write bit", fi.Mode().Perm())
		}
		out, werr := startAPIDAndExpectFail(t, append(envForAPID(t, poolDSN(pool)),
			"FAAS_HOST_AGE_RECIPIENT_PATH="+pub), 5*time.Second)
		// werr == nil means apid exited zero (clean), or failed to exit
		// (kill-and-reap). The helper signals bad-exit via non-nil werr
		// (proc.Wait returns *exec.ExitError when status != 0). The
		// previously-inverted check `if err != nil { Fatalf }` would
		// always trip on the success path — fix is to require the
		// non-nil exit error here.
		if werr == nil {
			t.Fatalf("apid should have exited non-zero with 0664 perms but exited cleanly:\n%s", out)
		}
		// Acceptable substrings:
		//   - the sentinel error text (LoadRecipient wraps it)
		//   - the wrapping fmt.Errorf from cmd/apid/main.go:312
		if !strings.Contains(out, "ErrRecipientInsecurePerms") &&
			!strings.Contains(out, "host.age.pub permissions") &&
			!strings.Contains(out, secretbox.ErrRecipientInsecurePerms.Error()) {
			t.Errorf("apid output did not mention insecure perms; output:\n%s", out)
		}
	})

	// §11 private half (host.age, NOT .pub). The earlier subtests
	// above cover apid's load of host.age.pub; this one covers
	// vmmd's load of host.age itself. apid never sees the private
	// half — that's the whole point of the split — so the
	// fail-fast path is exclusively on the vmmd side.
	//
	// Operator-drift tripwire: a chmod'd-loose host.age (group- or
	// world-readable) is the canonical "secret material has been
	// exposed" signal; secretbox.LoadHostKey returns
	// ErrHostKeyInsecurePerms, cmd/vmmd's loadOrGenerateHostIdentity
	// wraps it with %w, and vmmd's run() exits non-zero at
	// cmd/vmmd/main.go:175-177 BEFORE any KVM/listener/netns touch
	// (so the test runs on CI without /dev/kvm + root).
	//
	// The LoadHostKey contract is "exact 0o400 ONLY" — anything else
	// (group/other read, any write/exec/suid) is rejected. The
	// subtests below table-drive every non-0o400 mode that has
	// historically been the source of a perm-drift regression
	// (0o440 / 0o444 are the silent ones — group/world readable
	// without ever tripping a write-bit complaint from the file
	// audit). Earlier revisions only exercised 0o644, which left
	// the silent-drift modes uncovered.
	t.Run("rejects_insecure_private_perms", func(t *testing.T) {
		// Every mode below MUST trip ErrHostKeyInsecurePerms.
		// 0o400 (the production mode) is asserted separately
		// (vmmd should NOT fail-fast on it).
		insecureModes := []os.FileMode{
			0o440, // group-readable (silent-drift case #1)
			0o444, // world-readable (silent-drift case #2)
			0o460, // group-readable + group-list (operator ls accident)
			0o604, // owner-writable, other-readable
			0o640, // group-writable (operator multi-admin chmod)
			0o644, // canonical 0o644 (owner + group + other readable)
			0o660, // group-collaborative admin mistake
			0o666, // world-writable (the "I just want it to work" mistake)
			0o700, // owner-traversable — directory-style mistake
			0o755, // owner + group + other traversable
		}
		for _, mode := range insecureModes {
			mode := mode
			t.Run(fmt.Sprintf("mode_%04o", mode), func(t *testing.T) {
				dir := t.TempDir()
				key := filepath.Join(dir, "host.age")
				id, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatalf("GenerateX25519Identity: %v", err)
				}
				// writeWithPerm applies the exact requested mode bits
				// (umask-stripping is the GH/macOS foot-gun — see writeWithPerm).
				if err := writeWithPerm(t, key, []byte(id.String()), mode); err != nil {
					t.Fatalf("write host.age: %v", err)
				}
				fi, lerr := os.Stat(key)
				if lerr != nil {
					t.Fatalf("stat host.age: %v", lerr)
				}
				// Sanity: the mode on disk MUST match what we asked for.
				// If writeWithPerm silently stripped bits, the test
				// fixture became the wrong mode and would pass-vacuous.
				if fi.Mode().Perm() != mode {
					t.Fatalf("test setup failed: host.age mode=%04o, want %04o — writeWithPerm did not preserve the bits",
						fi.Mode().Perm(), mode)
				}
				// vmmd also needs the public recipient path even for the
				// fail-fast path so it can stop at the host.age check; it
				// IS allowed to write (LoadHostKey exits first), but we
				// write it pre-emptively to keep the seed symmetric with
				// production.
				pub := filepath.Join(dir, "host.age.pub")
				if err := writeWithPerm(t, pub, []byte(id.Recipient().String()), 0o444); err != nil {
					t.Fatalf("write host.age.pub: %v", err)
				}
				// vmmd.toml must exist (env default points at
				// /etc/faas/vmmd.toml which won't be writeable in CI).
				cfgDir := t.TempDir()
				cfgPath := filepath.Join(cfgDir, "vmmd.toml")
				if err := os.WriteFile(cfgPath, []byte(`socket_path = "`+filepath.Join(cfgDir, "vmmd.sock")+`"
owner_user = "root"
kernel_path = "/dev/null"
`), 0o600); err != nil {
					t.Fatalf("write vmmd.toml: %v", err)
				}
				out, werr := startVMMDAndExpectFail(t, []string{
					"PATH=" + os.Getenv("PATH"),
					"HOME=" + os.Getenv("HOME"),
					"FAAS_VMMD_CONFIG=" + cfgPath,
					"FAAS_HOST_KEY_PATH=" + key,
					"FAAS_HOST_AGE_RECIPIENT_PATH=" + pub,
					"FAAS_STORAGE_BACKEND=local",
				}, 10*time.Second)
				if werr == nil {
					t.Fatalf("vmmd should have exited non-zero with %04o host.age perms but exited cleanly:\n%s", mode, out)
				}
				// Acceptable substrings:
				//   - the sentinel ErrHostKeyInsecurePerms.Error()
				//   - the secretbox.LoadHostKey formatting ("host key ... mode ...")
				//   - cmd/vmmd/main.go:346 wrap prefix ("vmmd: host key")
				if !strings.Contains(out, secretbox.ErrHostKeyInsecurePerms.Error()) &&
					!strings.Contains(out, "host key") &&
					!strings.Contains(out, "ErrHostKeyInsecurePerms") {
					t.Errorf("vmmd output did not mention insecure host.age perms; output:\n%s", out)
				}
			})
		}
	})
}

// writeWithPerm writes data to path with the exact requested mode,
// bypassing the process umask. The umask matters here because the GH
// runner uses 0002 (test umask) while macOS dev defaults to 0022;
// os.WriteFile silently strips the unwanted bits so the test's intent
// (load a file LoadRecipient MUST reject) would pass-vacuous on macOS.
// This is the same shape pkg/secretbox.GenerateAndSaveHostKey uses (it
// does umask-strip-tolerant Chmod by re-applying bits).
func writeWithPerm(t *testing.T, path string, data []byte, perm os.FileMode) error {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

// --- TestSec11_PerHostEgressTemplating -----------------------------------
//
// §11 + ADR-055 — the per-host egress policy templating contract.
// Tier 1 Phase 4: a Hetzner compute node on a different NIC name
// (e.g. `ens5`) and/or a non-default masquerade CIDR
// (e.g. `10.101.0.0/16` for a second box's distinct RFC1918 slice)
// MUST render a ruleset that reflects the host's substitution values,
// and the Go render MUST byte-match the Jinja2 template render for
// every supported pair. The Jinja2 layer is a verifier, not a
// parallel writer — both surfaces are wired through the same
// substitution sites; any drift fails this test.
//
// The test is parametrised over the full {iface, cidr} matrix the
// multi-host rollout runbook (docs/runbooks/multi-host-rollout.md)
// explicitly calls out. Each iteration:
//
//  1. Renders the ruleset in-process via netns.HostPolicy.Render().
//  2. Substitutes the same pair into the Jinja2 template via the
//     stdlib jinja2 (Python's interpreter is the canonical verifier;
//     the Makefile target egress-render-cross-check uses the same
//     shape).
//  3. Asserts byte-equality across the two renderers — failure here
//     means the ansible-side template and the Go source of truth
//     have drifted (PR-A's load-bearing contract).
//
// Plus a separate subtest that pins the migration shape —
// `egress_policy` singleton row + the `egress_policy_changed`
// pg_notify channel — so a future migration that drops the audit
// table or the channel name breaks this test before it breaks
// `cmd/vmmd/egress_watcher.go` at runtime.
//
// Skips cleanly when jinja2 is not importable (macOS dev without
// `pip install jinja2`). On a CI runner with jinja2 installed, the
// gate is run on every push per `make egress-render-cross-check`.
//
// The Go-only part of the test (no jinja2 dependency) covers the
// table-driven matrix even on jinja2-less hosts: the e2e sweep runs
// on bare metal, lima, and CI runners, and a CI runner that lacks
// the Python dep should still pin the Go-side substitution.

func TestSec11_PerHostEgressTemplating(t *testing.T) {
	if _, err := repoRootIfReachable(); err != nil {
		t.Skipf("module root not reachable from cwd: %v", err)
	}
	root := repoRoot()

	// The matrix matches what docs/runbooks/multi-host-rollout.md
	// expects in production: the default-local node on eth0 +
	// 10.100.0.0/16, plus a Hetzner compute node on ens5 +
	// 10.101.0.0/16. The cross-product catches a single-axis
	// regression (e.g. only the iface was parameterised; the cidr
	// stayed hardcoded).
	matrix := []struct {
		name   string
		iface  string
		cidr   string
		eth0OK bool // is the canonical-default iface OK to appear?
		cidrOK bool // is the canonical-default CIDR OK to appear?
	}{
		{name: "default_local_eth0", iface: "eth0", cidr: "10.100.0.0/16", eth0OK: true, cidrOK: true},
		{name: "fsn2_ens5", iface: "ens5", cidr: "10.101.0.0/16", eth0OK: false, cidrOK: false},
	}

	jinjaOK := jinja2Available()

	for _, tc := range matrix {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			policy := netns.DefaultHostPolicy
			policy.PublicIface = tc.iface
			policy.MasqueradeCIDR = tc.cidr
			out := policy.Render()

			// 1. Forward chain picks up the new iface. The rule is
			//    the only place in the rendered output where
			//    PublicIface appears in a forward-chain rule; an
			//    operator inspecting the live nftables sees this
			//    rule as the "tenant bridge → public iface" permit.
			wantFwd := fmt.Sprintf(`iifname %q oifname %q accept`,
				netns.TenantBridge, tc.iface)
			if !strings.Contains(out, wantFwd) {
				t.Errorf("forward chain did not pick up public_iface substitution:\n  want %q\n  got  %s",
					wantFwd, out)
			}

			// 2. Postrouting MASQUERADE rule picks up both fields.
			wantMasq := fmt.Sprintf(`ip saddr %s oifname %q masquerade`,
				tc.cidr, tc.iface)
			if !strings.Contains(out, wantMasq) {
				t.Errorf("postrouting chain did not pick up masquerade_cidr/public_iface substitution:\n  want %q\n  got  %s",
					wantMasq, out)
			}

			// 3. Anti-regression: the production default must NOT
			//    appear once the field is varied. Scoped to the
			//    literal `oifname "eth0"` token (so an unrelated
			//    `eth0` substring in a comment doesn't false-positive)
			//    and to the literal `ip saddr 10.100.0.0/16` for the
			//    CIDR (same scoping rationale).
			if !tc.eth0OK && strings.Contains(out, `oifname "eth0"`) {
				t.Errorf("rendered output retained default iface when test varied it:\n  iface=%s\n  %s",
					tc.iface, out)
			}
			if !tc.cidrOK && strings.Contains(out, "ip saddr 10.100.0.0/16") {
				t.Errorf("rendered output retained default CIDR when test varied it:\n  cidr=%s\n  %s",
					tc.cidr, out)
			}

			// 4. Cross-check against the Jinja2 template (the
			//    ansible-side verifier). Both surfaces MUST produce
			//    byte-identical output for every matrix entry — the
			//    Jinja2 layer is a verifier, not a parallel writer.
			//    Without this assertion, a Jinja2 drift (e.g. an
			//    extra `oifname` token) would silently ship a ruleset
			//    the operator wouldn't be able to reproduce from the
			//    Go source of truth.
			if !jinjaOK {
				t.Logf("jinja2 not importable on this host; skipping Go-vs-Jinja2 cross-check for %s", tc.name)
				return
			}
			// Mega-PR-C Commit 2 moved the Jinja2 template from
			// `nftables/files/` to `nftables/templates/` so ansible's
			// `template:` module resolves it (the deployctl generator
			// also lives on the templates/ side). Cross-check
			// against the canonical ansible input, not a stale copy.
			tmplPath := filepath.Join(root,
				"deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2")
			tmplBytes, err := os.ReadFile(tmplPath)
			if err != nil {
				t.Fatalf("read Jinja2 template %s: %v", tmplPath, err)
			}
			jOut, err := renderJinja2(string(tmplBytes), tc.iface, tc.cidr)
			if err != nil {
				t.Fatalf("render Jinja2 template: %v", err)
			}
			// Cross-check invariant: the rendered bodies must match.
			// The Makefile target `egress-render-cross-check` uses
			// shell-command-substitution semantics, which strip a
			// trailing newline from the command output. The Go render
			// always emits a final `\n`; Jinja2 by default does not
			// emit a trailing newline after `}}`. Normalize both
			// trailing newlines to a single canonical form so the
			// comparison surfaces real drift (a body-byte mismatch),
			// not the trivial final-newline cosmetic.
			outNorm := strings.TrimRight(out, "\n") + "\n"
			jOutNorm := strings.TrimRight(jOut, "\n") + "\n"
			if outNorm != jOutNorm {
				// Surface the first divergent line so a reviewer's
				// eye lands on the change (same shape as
				// sec11_host_linux_test.go::TestSec11_NftablesPolicyIsArtifactInSync).
				outLines := strings.Split(outNorm, "\n")
				jOutLines := strings.Split(jOutNorm, "\n")
				for i := 0; i < len(outLines) && i < len(jOutLines); i++ {
					if outLines[i] != jOutLines[i] {
						t.Errorf("Go vs Jinja2 render diverges at line %d for iface=%s cidr=%s:\n  go   : %s\n  jinja: %s",
							i+1, tc.iface, tc.cidr, outLines[i], jOutLines[i])
						break
					}
				}
				if len(outLines) != len(jOutLines) {
					t.Errorf("Go vs Jinja2 render line count differs for iface=%s cidr=%s: go=%d jinja=%d",
						tc.iface, tc.cidr, len(outLines), len(jOutLines))
				}
			}
		})
	}
}

// jinja2Available reports whether the local Python interpreter can
// import jinja2. Used by TestSec11_PerHostEgressTemplating to skip
// the Jinja2 cross-check on hosts without jinja2 installed (macOS
// dev) while still running the Go-side matrix.
func jinja2Available() bool {
	cmd := exec.Command("python3", "-c", "import jinja2")
	cmd.Stderr = io.Discard
	cmd.Stdout = io.Discard
	return cmd.Run() == nil
}

// renderJinja2 runs the jinja2 renderer on the given template body
// with the given public_iface/masquerade_cidr overrides. Mirrors the
// Makefile target `egress-render-cross-check`'s shape: a one-shot
// Python invocation that produces the rendered body on stdout.
//
// The Python script is invoked as `python3 -c <script>` with the
// template body fed via stdin. The two substitution values are
// passed as the script's argv (positional after `-c`'s script arg)
// — this avoids inline-interpolation hazards (no `%q` on the shell
// side; no quoting-of-quoting through Go's exec.Command).
//
// The script reads the template body from stdin, the two values
// from sys.argv[1] and sys.argv[2], and writes the rendered body
// to stdout. jinja2.Template.render is the canonical entry point.
func renderJinja2(tmplBody, publicIface, masqueradeCIDR string) (string, error) {
	script := `import sys
from jinja2 import Template
tmpl = Template(sys.stdin.read())
print(tmpl.render(public_iface=sys.argv[1], masquerade_cidr=sys.argv[2]), end="")
`
	cmd := exec.Command("python3", "-c", script, publicIface, masqueradeCIDR)
	cmd.Stdin = strings.NewReader(tmplBody)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("python3 render: %w: %s", err, errBuf.String())
	}
	return out.String(), nil
}

// repoRootIfReachable wraps repoRoot with an error-return so the
// caller can distinguish "module root not reachable" (skip) from
// "module root reachable but template missing" (fail). The
// non-error return shape is fine in places where we want to fall
// back on "" (sec11_host_linux_test.go) but the e2e sweep test
// here needs to skip cleanly without a t.Skipf at every call site.
func repoRootIfReachable() (string, error) {
	root := repoRoot()
	if root == "" {
		return "", fmt.Errorf("module root not found by walking up from cwd")
	}
	return root, nil
}

// --- TestSec11_EgressPolicyMigrationShape --------------------------------
//
// ADR-055 + migration 00078: the `egress_policy` audit table and
// its `egress_policy_changed` pg_notify channel are the runtime
// contract cmd/vmmd/egress_watcher.go subscribes to. Pin both the
// table shape and the channel name so a future migration that drops
// the channel or renames the table breaks this test before it
// breaks the watcher at startup.
//
// The test runs against a pgtest-backed pool when DATABASE_URL is
// reachable; otherwise it skips. Skipping is the canonical behavior
// on macOS dev where there is no Postgres reachable — the e2e
// gate runs on every CI push via the postgres service container.

func TestSec11_EgressPolicyMigrationShape(t *testing.T) {
	pool := openSchemaPG(t)

	// 1. The egress_policy table exists with the singleton PK.
	//    We don't assert column count here — migration 00078 may grow
	//    columns under future ADRs (e.g. per-tenant policy) and the
	//    test should track that without churn. We DO assert the
	//    singleton CHECK constraint, which is load-bearing for the
	//    watcher's expectation of "exactly one row".
	// Scope the catalog lookup to current_schema(). The bare
	// `pg_class.relname = 'egress_policy'` shape trips a latent
	// flake under pgtest.Open: each test schema is isolated but
	// pg_class is cluster-wide, so a sibling test that has
	// (transiently) created+dropped an egress_policy in another
	// schema leaves the OID visible at plan time, then
	// pg_get_constraintdef raises "could not open relation with
	// OID N" at execution time. Same root cause as memory
	// migrations-info-schema-scoping-pattern (PR #339) — every
	// information_schema / pg_catalog lookup in a migration-apply
	// test must filter on the test's own schema. Scoping by
	// `t.relnamespace = (SELECT oid FROM pg_namespace WHERE
	// nspname = current_schema())` matches the canonical pattern
	// and reads as a single self-contained predicate.
	var constraintDef string
	err := pool.QueryRow(context.Background(),
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE n.nspname = current_schema()
		    AND t.relname = 'egress_policy'
		    AND c.contype = 'c'
		    AND c.conname = 'egress_policy_singleton'`,
	).Scan(&constraintDef)
	if err != nil {
		t.Fatalf("query egress_policy_singleton constraint: %v", err)
	}
	// pg_get_constraintdef emits CHECK (id = 'singleton') — see
	// memory "pg_get_constraintdef CHECK shapes" (the only legal
	// shape is IN/ANY).
	if !strings.Contains(constraintDef, "singleton") {
		t.Errorf("egress_policy_singleton CHECK shape unexpected: %q", constraintDef)
	}

	// 2. The seeded singleton row exists with the canonical defaults.
	var publicIface, masqueradeCIDR string
	err = pool.QueryRow(context.Background(),
		`SELECT public_iface, masquerade_cidr FROM egress_policy WHERE id = 'singleton'`,
	).Scan(&publicIface, &masqueradeCIDR)
	if err != nil {
		t.Fatalf("query egress_policy singleton row: %v", err)
	}
	if publicIface != "eth0" {
		t.Errorf("egress_policy.public_iface = %q, want eth0", publicIface)
	}
	if masqueradeCIDR != api.DefaultMasqueradeCIDR {
		t.Errorf("egress_policy.masquerade_cidr = %q, want %s", masqueradeCIDR, api.DefaultMasqueradeCIDR)
	}

	// 3. The pg_notify channel fires when the audit row is updated.
	//    Use LISTEN + UPDATE inside a single tx so the channel
	//    notification is delivered before we read it. The test
	//    asserts the JSON payload's shape matches what
	//    cmd/vmmd/egress_watcher.go expects to decode.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe to the channel. pgx exposes LISTEN via a dedicated
	// connection (not the pool) — open a dedicated conn from the
	// pool to call LISTEN/WAIT/UNLISTEN atomically.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn for LISTEN: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN egress_policy_changed`); err != nil {
		t.Fatalf("LISTEN egress_policy_changed: %v", err)
	}

	// Touch the audit row from a separate pool connection so the
	// notification is delivered. Re-POST with the same values — the
	// trigger fires AFTER UPDATE regardless of value change, so the
	// test doesn't need to mutate the production defaults.
	if _, err := pool.Exec(ctx,
		`UPDATE egress_policy SET public_iface = 'eth0' WHERE id = 'singleton'`); err != nil {
		t.Fatalf("UPDATE egress_policy: %v", err)
	}

	// Wait for the notification. pgx surfaces notifications through
	// (*pgx.Conn).WaitForNotification(ctx); the timeout bounds the
	// wait so a missing-trigger regression fails fast (rather than
	// hanging the test).
	notif, err := conn.Conn().WaitForNotification(ctx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v", err)
	}
	if notif.Channel != db.NotifyEgressPolicyChanged {
		t.Errorf("notify channel = %q, want %q", notif.Channel, db.NotifyEgressPolicyChanged)
	}
	// Payload must contain the three fields the watcher decodes.
	var payload struct {
		PolicyID       string `json:"policy_id"`
		PublicIface    string `json:"public_iface"`
		MasqueradeCIDR string `json:"masquerade_cidr"`
		ChangedAt      string `json:"changed_at"`
	}
	if err := json.Unmarshal([]byte(notif.Payload), &payload); err != nil {
		t.Fatalf("decode notify payload: %v (payload=%s)", err, notif.Payload)
	}
	if payload.PolicyID != "singleton" {
		t.Errorf("payload.policy_id = %q, want singleton", payload.PolicyID)
	}
	if payload.PublicIface != "eth0" {
		t.Errorf("payload.public_iface = %q, want eth0", payload.PublicIface)
	}
	if payload.MasqueradeCIDR != api.DefaultMasqueradeCIDR {
		t.Errorf("payload.masquerade_cidr = %q, want %s", payload.MasqueradeCIDR, api.DefaultMasqueradeCIDR)
	}
	if payload.ChangedAt == "" {
		t.Errorf("payload.changed_at empty; want non-empty timestamptz echo")
	}
}

// --- TestSec11_NftablesArtifactGate moved to sec11_host_linux_test.go ---
//
// §11 "nftables default-drop inbound" needed CAP_NET_ADMIN and a host
// kernel to be exercised live, so it lives in the linux-only file.
// sec11_host_linux_test.go::TestSec11_NftablesPolicyIsArtifactInSync
// byte-compares the rendered output of pkg/netns.DefaultHostPolicy()
// against the committed deploy/ansible/roles/nftables/files/* artifact
// — the same gate `make egress-check` enforces, without the per-test
// `go run ./cmd/faas-nft-render` shell-out.
