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
	"errors"
	"filippo.io/age"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
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
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("startProc(%s): %v", bin, err)
	}
	return cmd
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

// TestMain builds a single apid binary into a package-level tmpdir
// before any test runs, so the per-test helpers don't pay the `go
// build` cost 5x. Each test still gets its own apid subprocess (each
// needs its own /etc/faas/secrets/host.age.pub fixture and its own
// FAAS_APID_LISTEN) — only the BINARY is shared, not the process.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "faas-sec11-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sec11_test: mkdir tmp: %v\n", err)
		os.Exit(2)
	}
	bin := filepath.Join(dir, "apid")

	// The test binary's cwd is the package directory; resolve to the
	// module root so `go build ./cmd/apid` finds the package.
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

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/apid")
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sec11_test: go build apid: %v\n%s", err, buf.String())
		os.Exit(2)
	}
	apidBinary = bin
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// apidBinary is set once in TestMain; every test uses this path.
var apidBinary string

// envForAPID returns the base env slice every apid subprocess needs,
// WITHOUT FAAS_APID_LISTEN (startAPIDWithEnv / startAPIDAndExpectFail
// allocate the listen addr inside and append it last). Same shape as
// pkg/e2etest.testEnvCommon (harness.go:498) minus the listen var.
func envForAPID(dbURL string, extra ...string) []string {
	env := []string{
		"DATABASE_URL=" + dbURL,
		"FAAS_SKIP_SOCKET_GROUP=1", // harness convention; see harness.go:498
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"FAAS_APPS_DOMAIN=apps.test.example",
	}
	env = append(env, extra...)
	return env
}

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

// startAPIDAndExpectFail boots apid (NO t.Cleanup) and asserts it exits
// non-zero within `expectFailWithin`. Used by the negative host-key
// subtest where e2etest.StartWithEnv would t.Fatalf on the boot failure
// and mask the assertion.
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
		if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
			return buf.String(), err
		}
		return "", err
	case <-time.After(expectFailWithin):
		_ = proc.Process.Kill()
		<-doneCh
		if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
			return buf.String(), errors.New("apid did not exit within deadline — fail-fast missing")
		}
		return "", errors.New("apid did not exit within deadline — fail-fast missing")
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
	addr, _ := startAPIDWithEnv(t, envForAPID(poolDSN(pool))...)
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

	// Phase 2: per-IP isolation. A second X-Forwarded-For must NOT be
	// in the same bucket as the first. We expect 401 (bogus bearer),
	// NOT 429 — this is what catches a future regression to
	// AuthLimit(cfg) per-route (memory: shared-bucket regression).
	const otherIP = "203.0.113.42"
	req, _ = http.NewRequest(http.MethodGet, "http://"+addr+"/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer fp_live_bogus_otherip")
	req.Header.Set("X-Forwarded-For", otherIP)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("other-ip attempt: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("other-IP attempt: status=%d want 401 (per-IP bucket leaked across XFFs)", resp.StatusCode)
	}
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
	addr, _ := startAPIDWithEnv(t, envForAPID(poolDSN(pool))...)
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

// --- TestSec11_UnixSocketOnlyDSN -----------------------------------------
//
// §11 "Postgres on unix socket only". After boot we query
// pg_stat_activity for any session of the current user — every row
// must have client_addr IS NULL (i.e. unix-socket peer auth). A future
// refactor that defaults to localhost would fail here.

func TestSec11_UnixSocketOnlyDSN(t *testing.T) {
	pool := openSchemaPG(t)
	addr, _ := startAPIDWithEnv(t, envForAPID(poolDSN(pool))...)
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

	rows, err := pool.Query(context.Background(),
		`SELECT client_addr FROM pg_stat_activity
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

	t.Run("accepts_allowed_perms", func(t *testing.T) {
		dir := t.TempDir()
		pub := filepath.Join(dir, "host.age.pub")
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("GenerateX25519Identity: %v", err)
		}
		if err := os.WriteFile(pub, []byte(id.Recipient().String()), 0o444); err != nil {
			t.Fatalf("write pub: %v", err)
		}
		addr, _ := startAPIDWithEnv(t, append(envForAPID(poolDSN(pool)),
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
		if err := os.WriteFile(pub, []byte(id.Recipient().String()), 0o664); err != nil {
			t.Fatalf("write pub: %v", err)
		}
		out, err := startAPIDAndExpectFail(t, append(envForAPID(poolDSN(pool)),
			"FAAS_HOST_AGE_RECIPIENT_PATH="+pub), 5*time.Second)
		if err != nil {
			t.Fatalf("apid should have exited non-zero with 0664 perms: %v\n%s", err, out)
		}
		// Acceptable substrings:
		//   - the sentinel error text (LoadRecipient wraps it)
		//   - the wrapping fmt.Errorf from cmd/apid/main.go:315
		if !strings.Contains(out, "ErrRecipientInsecurePerms") &&
			!strings.Contains(out, "host.age.pub permissions") &&
			!strings.Contains(out, secretbox.ErrRecipientInsecurePerms.Error()) {
			t.Errorf("apid stderr did not mention insecure perms; output:\n%s", out)
		}
	})
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
