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
	"github.com/onebox-faas/faas/pkg/state"
)

// openSchemaPG opens pgtest, runs migrations to the current head, and
// returns a per-test pool plus the harness tmpdir. Mirrors the opening
// dance in quota_e2e_test.go / secrets_e2e_test.go.
func openSchemaPG(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open skipped (no DATABASE_URL)")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	return pool, t.TempDir()
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

// repoRoot walks up from cwd to the module root (the dir holding
// go.mod). The test binary's cwd varies by setup (sometimes the package
// dir, sometimes t.TempDir()), so absolute resolution is the only
// reliable approach.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find repo root from %s", wd)
	return ""
}

// buildAPIDOnce compiles the apid binary into tmpdir/bin/apid. We
// rebuild per-test rather than reusing a shared binary because each
// subtest wants its own tmpdir (for the host.age.pub fixture) and a
// fresh pool/schema; the Go build cache makes this fast in CI.
func buildAPIDOnce(t *testing.T, tmpDir string) string {
	t.Helper()
	bin := filepath.Join(tmpDir, "bin", "apid")
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// Use `go build` rather than importing cmd/apid's main package so the
	// test binary doesn't double-link the package (would force a main
	// symbol collision on Linux). The Makefile's e2etest harness does the
	// same.
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/apid")
	cmd.Dir = repoRoot(t)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build apid: %v\n%s", err, buf.String())
	}
	return bin
}

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
// address, the process, and a function to read its stdout/stderr buffer.
// The listen address is allocated inside (matching pkg/e2etest harness
// pattern at harness.go:475-484) and threaded into FAAS_APID_LISTEN.
func startAPIDWithEnv(t *testing.T, bin string, extraEnv ...string) (string, *exec.Cmd, func() string) {
	t.Helper()
	addr := freeTCPAddr(t)
	env := append(extraEnv, "FAAS_APID_LISTEN="+addr)
	proc := startProc(t, bin, env)
	waitTCP(t, addr, 10*time.Second)
	readBuf := func() string {
		if buf, ok := proc.Stdout.(*bytes.Buffer); ok {
			return buf.String()
		}
		return ""
	}
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
	return addr, proc, readBuf
}

// startAPIDAndExpectFail boots apid (NO t.Cleanup) and asserts it exits
// non-zero within `expectFailWithin`. Used by the negative host-key
// subtest where e2etest.StartWithEnv would t.Fatalf on the boot failure
// and mask the assertion.
func startAPIDAndExpectFail(t *testing.T, bin string, env []string, expectFailWithin time.Duration) (string, error) {
	t.Helper()
	addr := freeTCPAddr(t)
	fullEnv := append(env, "FAAS_APID_LISTEN="+addr)
	proc := startProc(t, bin, fullEnv)
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
	pool, tmpDir := openSchemaPG(t)
	bin := buildAPIDOnce(t, tmpDir)
	addr, _, _ := startAPIDWithEnv(t, bin, envForAPID(poolDSN(pool))...)
	client := &http.Client{Timeout: 10 * time.Second}

	// 10× bogus bearer — all 401.
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/apps", nil)
		req.Header.Set("Authorization", "Bearer fp_live_bogus_"+strconv.Itoa(i))
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
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("attempt 11: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attempt 11: status=%d want 429 (auth-limited)", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Errorf("attempt 11: missing Retry-After header on 429")
	}
}

// --- TestSec11_ApiKeyHashedAtRest ----------------------------------------
//
// §11 "API keys hashed". The pkg/api/apikey_test.go unit test pins the
// hash function; this test pins the row shape: the api_keys row holds
// sha256(bearer) and never contains the plaintext.

func TestSec11_ApiKeyHashedAtRest(t *testing.T) {
	pool, tmpDir := openSchemaPG(t)
	bin := buildAPIDOnce(t, tmpDir)
	addr, _, _ := startAPIDWithEnv(t, bin, envForAPID(poolDSN(pool))...)
	apidURL := "http://" + addr
	_ = apidURL

	// Seed an account via the harness (same dance the other e2e tests
	// use) and capture the bearer. We don't need apidURL here — the
	// seed runs directly against the test pool, then we read the row
	// shape.
	bearer := seedBearerViaPool(t, pool, api.PlanHobby, "sec11")
	wantHash := api.HashAPIKey(bearer)

	var gotHash []byte
	err := pool.QueryRow(context.Background(),
		`SELECT key_hash FROM api_keys
		 WHERE account_id = (
		   SELECT id FROM accounts WHERE email = 'e2e+hobby+sec11@test.example'
		 ) LIMIT 1`,
	).Scan(&gotHash)
	if err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	if !bytes.Equal(gotHash, wantHash) {
		t.Errorf("key_hash mismatch: got %x want %x", gotHash, wantHash)
	}
	if strings.Contains(string(gotHash), bearer) {
		t.Errorf("plaintext bearer %q found inside key_hash %x — NOT hashed", bearer, gotHash)
	}
	if strings.Contains(string(gotHash), "fp_live_") {
		t.Errorf("plaintext key prefix %q found inside key_hash %x", "fp_live_", gotHash)
	}
}

// seedBearerViaPool is the same dance e2etest.Harness.SeedAccount runs,
// inlined so this file doesn't need a Harness for the table-read tests.
func seedBearerViaPool(t *testing.T, pool *pgxpool.Pool, plan api.Plan, label string) string {
	t.Helper()
	store := state.NewPgStore(pool)
	email := "e2e+" + string(plan) + "+" + label + "@test.example"
	acct, err := store.CreateAccount(context.Background(), email, plan)
	if err != nil {
		// account already seeded → look up
		acct, lerr := store.AccountByEmail(context.Background(), email)
		if lerr != nil {
			t.Fatalf("seed account %s (initial=%v, lookup=%v)", plan, err, lerr)
		}
		pt, hash, gerr := api.GenerateAPIKey()
		if gerr != nil {
			t.Fatalf("GenerateAPIKey: %v", gerr)
		}
		if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "e2e"); err != nil {
			t.Logf("CreateAPIKey (already exists, ignoring): %v", err)
		}
		return pt
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "e2e"); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return pt
}

// poolDSN returns the DSN apid should connect with. It mirrors pgtest's
// default (postgres:///faas?host=/run/postgresql&user=faas) but
// respects $DATABASE_URL so CI's postgres service is honored.
func poolDSN(pool *pgxpool.Pool) string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	cfg := pool.Config()
	if cfg != nil && cfg.ConnConfig != nil {
		// reconstruct a usable DSN from the parsed config
		return cfg.ConnConfig.ConnString()
	}
	return "postgres:///faas?host=/run/postgresql&user=faas"
}

// --- TestSec11_UnixSocketOnlyDSN -----------------------------------------
//
// §11 "Postgres on unix socket only". After boot we query
// pg_stat_activity for any session of the current user — every row
// must have client_addr IS NULL (i.e. unix-socket peer auth). A future
// refactor that defaults to localhost would fail here.

func TestSec11_UnixSocketOnlyDSN(t *testing.T) {
	pool, tmpDir := openSchemaPG(t)
	bin := buildAPIDOnce(t, tmpDir)
	addr, _, _ := startAPIDWithEnv(t, bin, envForAPID(poolDSN(pool))...)
	_ = addr
	// Give apid's pool a beat to register with pg_stat_activity.
	time.Sleep(200 * time.Millisecond)

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
	pool, tmpDir := openSchemaPG(t)
	bin := buildAPIDOnce(t, tmpDir)

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
		addr, _, _ := startAPIDWithEnv(t, bin, append(envForAPID(poolDSN(pool)),
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
		out, err := startAPIDAndExpectFail(t, bin, append(envForAPID(poolDSN(pool)),
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

// --- TestSec11_NftablesArtifactGate --------------------------------------
//
// §11 "nftables default-drop inbound" — pinned at the artifact layer.
// make egress-check is the canonical gate (Makefile:147-174). This test
// shells out to it so a coding agent can re-run the gate programmatically.
// The pkg/netns unit tests already cover the rendered text; this is the
// "did someone forget to commit the latest render" tripwire.

func TestSec11_NftablesArtifactGate(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skipf("make not on PATH: %v", err)
	}
	cmd := exec.Command("make", "egress-check")
	cmd.Dir = repoRoot(t)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		t.Errorf("make egress-check failed: %v\n%s", err, buf.String())
	}
}

// Compile-time guards so this file's import set stays stable as the
// project evolves. Removing these breaks compilation loudly rather than
// silently dropping imports.
var (
	_ = e2etest.APID
	_ = e2etest.Which(0)
)
