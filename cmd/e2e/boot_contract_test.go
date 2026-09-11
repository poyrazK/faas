package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	imagedpkg "github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/renderer"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestBootContract_APIDRenderedConfigAndProductionListeners is the first
// issue-#1529 boot-contract pin. It executes the real apid binary from the
// manifest-rendered TOML and systemd environment contract, leaves the
// production-default gRPC surfaces enabled, and waits for dependency-aware
// readiness. Only ports, sockets, credentials, and writable paths are moved
// into test-owned directories.
func TestBootContract_APIDRenderedConfigAndProductionListeners(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open skipped")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate isolated boot-contract schema: %v", err)
	}

	dsn := poolDSN(pool)
	manifestPath := writeRenderManifestWithDSN(t, dsn)
	renderRoot := t.TempDir()
	etcDir := filepath.Join(renderRoot, "etc")
	systemdDir := filepath.Join(renderRoot, "systemd")
	_, err := renderer.Render(renderer.RenderOptions{
		ManifestPath: manifestPath,
		ReleasesRoot: filepath.Join(renderRoot, "releases"),
		EtcFaasDir:   etcDir,
		SystemdDir:   systemdDir,
		PKIRootDir:   filepath.Join(renderRoot, "tls"),
		CgroupRoot:   filepath.Join(renderRoot, "cgroup"),
	})
	if err != nil {
		t.Fatalf("render production manifest: %v", err)
	}

	unitBody, err := os.ReadFile(filepath.Join(systemdDir, "faas-apid.service"))
	if err != nil {
		t.Fatalf("read rendered apid unit: %v", err)
	}
	unit, err := daemonunit.Decode(unitBody)
	if err != nil {
		t.Fatalf("decode rendered apid unit: %v", err)
	}
	configPath := renderedConfigPath(t, unit, etcDir, "apid")
	relocateRenderedDBURL(t, configPath, dsn)

	socketDir, err := os.MkdirTemp("", "faas-boot-apid-*")
	if err != nil {
		t.Fatalf("make short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

	sessionKeyPath := filepath.Join(socketDir, "session.key")
	hostHMACPath := filepath.Join(socketDir, "host.hmac.key")
	hostAgePath := filepath.Join(socketDir, "host.age")
	writeBootKey(t, sessionKeyPath, []byte(randomHexKey(t)), 0o400)
	writeBootKey(t, hostHMACPath, randomBytes(t, 32), 0o400)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate host age identity: %v", err)
	}
	// GenerateAndSaveHostKey writes the raw age identity with no trailing
	// newline; keep the boot fixture byte-for-byte equivalent because the
	// production loader intentionally parses the credential strictly.
	writeBootKey(t, hostAgePath, []byte(identity.String()), 0o400)

	mainAddr := freeTCPAddr(t)
	controlAddr := freeTCPAddr(t)
	advisorySocket := filepath.Join(socketDir, "advisory.sock")
	appErrorsSocket := filepath.Join(socketDir, "app-errors.sock")
	requestTelemetrySocket := filepath.Join(socketDir, "requests.sock")
	authSocket := filepath.Join(socketDir, "auth.sock")
	spansSocket := filepath.Join(socketDir, "spans.sock")

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + renderRoot,
		"TMPDIR=" + socketDir,
		// The production unit loads /etc/faas/sealed.env, whose deployment
		// contract requires DATABASE_URL even when apid.toml carries the local
		// control-plane socket DSN. Use the isolated test DSN for that entry.
		"DATABASE_URL=" + dsn,
		"FAAS_SKIP_SOCKET_GROUP=1",
		"FAAS_APID_LISTEN=" + mainAddr,
		"FAAS_APID_METRICS_ADDR=" + controlAddr,
		"FAAS_APID_APP_ERRORS_TARGET=" + appErrorsSocket,
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET=" + requestTelemetrySocket,
		"FAAS_APID_AUTH_SOCKET=" + authSocket,
		"FAAS_APID_OTEL_SPANS_WRITER_SOCKET=" + spansSocket,
		"FAAS_SPOOL_ROOT=" + filepath.Join(renderRoot, "spool"),
		"FAAS_SCAN_SPOOL_ROOT=" + filepath.Join(renderRoot, "scan-spool"),
		"FAAS_GITHUBD_STAGING_ROOT=" + filepath.Join(renderRoot, "githubd-staging"),
		"FAAS_MFA_RECOVERY_HMAC_KEY=" + randomHexKey(t),
		"FAAS_AUDIT_HMAC_KEY=" + randomHexKey(t),
		"FAAS_MAIL_TRANSPORT=log",
		"FAAS_BILLING_PROVIDER=paddle",
		"FAAS_PADDLE_SANDBOX=1",
		"FAAS_PADDLE_API_KEY=pdl_test_boot_contract",
		"FAAS_PADDLE_WEBHOOK_SECRET=whk_test_boot_contract",
	}
	env = append(env, renderedUnitEnvironment(t, unit, sessionKeyPath, hostAgePath, hostHMACPath, advisorySocket, renderRoot)...)

	proc := exec.Command(apidBinary, "--config", configPath)
	proc.Env = env
	var logs syncBuffer
	proc.Stdout = &logs
	proc.Stderr = &logs
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := proc.Start(); err != nil {
		t.Fatalf("start rendered apid: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil && !t.Failed() {
				t.Errorf("apid exited early: %v\n%s", err, logs.String())
			}
			return
		default:
		}
		_ = syscall.Kill(-proc.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-proc.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})

	waitBootReady(t, "http://"+controlAddr+"/readyz", 15*time.Second, &logs)
	waitTCP(t, mainAddr, 2*time.Second)
	for name, path := range map[string]string{
		"advisory":          advisorySocket,
		"app-errors":        appErrorsSocket,
		"request-telemetry": requestTelemetrySocket,
		"auth":              authSocket,
		"spans-writer":      spansSocket,
	} {
		assertUnixAccepts(t, name, path, &logs)
	}
}

// TestBootContract_ImagedRenderedConfigAndFunctionRunners executes imaged's
// real production entrypoint with its manifest-rendered TOML and systemd
// environment. The runner paths come exclusively from UnitImaged: reverting
// production-fix commit 7d76deaf0 (issue #1286) therefore makes this boot fail
// before readiness instead of allowing function deploys to break in the fleet.
func TestBootContract_ImagedRenderedConfigAndFunctionRunners(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open skipped")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate isolated boot-contract schema: %v", err)
	}

	dsn := poolDSN(pool)
	manifestPath := writeRenderManifestWithDSN(t, dsn)
	renderRoot := t.TempDir()
	etcDir := filepath.Join(renderRoot, "etc")
	systemdDir := filepath.Join(renderRoot, "systemd")
	_, err := renderer.Render(renderer.RenderOptions{
		ManifestPath: manifestPath,
		ReleasesRoot: filepath.Join(renderRoot, "releases"),
		EtcFaasDir:   etcDir,
		SystemdDir:   systemdDir,
		PKIRootDir:   filepath.Join(renderRoot, "tls"),
		CgroupRoot:   filepath.Join(renderRoot, "cgroup"),
	})
	if err != nil {
		t.Fatalf("render production manifest: %v", err)
	}

	unitBody, err := os.ReadFile(filepath.Join(systemdDir, "faas-imaged.service"))
	if err != nil {
		t.Fatalf("read rendered imaged unit: %v", err)
	}
	unit, err := daemonunit.Decode(unitBody)
	if err != nil {
		t.Fatalf("decode rendered imaged unit: %v", err)
	}
	nodeName := requiredUnitEnvironmentValue(t, unit, "FAAS_NODE_NAME")
	if _, err := state.NewPgStore(pool).CreateComputeNode(context.Background(), state.ComputeNode{
		Name:               nodeName,
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             1,
		MemMB:              1024,
		MaxConcurrency:     1,
		AdmissionCeilingMB: 512,
		VCPUBudget:         1,
		Active:             true,
	}); err != nil {
		t.Fatalf("seed rendered imaged owner node %q: %v", nodeName, err)
	}
	configPath := renderedConfigPath(t, unit, etcDir, "imaged")
	controlAddr := freeTCPAddr(t)
	relocateRenderedMetricsAddr(t, configPath, controlAddr)

	fixtureRoot := t.TempDir()
	hostAgePath := filepath.Join(fixtureRoot, "host.age")
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate host age identity: %v", err)
	}
	writeBootKey(t, hostAgePath, []byte(identity.String()), 0o400)

	guestInitPath := filepath.Join(fixtureRoot, "guest-init")
	guestInitBody := []byte("#!/bin/sh\nexit 0\n")
	writeBootKey(t, guestInitPath, guestInitBody, 0o755)
	privPEM, _, err := cosign.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate imaged signing key: %v", err)
	}
	signKeyPath := filepath.Join(fixtureRoot, "sign.key")
	writeBootKey(t, signKeyPath, privPEM, 0o400)

	storageRoot := filepath.Join(fixtureRoot, "storage")
	seedBootContractBuilderBase(t, storageRoot, guestInitBody)
	registry := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(registry.Close)
	fakeBin := filepath.Join(fixtureRoot, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("make boot-contract bin dir: %v", err)
	}
	// imaged validates an existing base read-only through debugfs. The boot
	// contract is about daemon/deployment wiring, so use a deterministic shim
	// instead of constructing a multi-gigabyte ext4 on every CI run.
	writeBootKey(t, filepath.Join(fakeBin, "debugfs"), []byte("#!/bin/sh\necho 'Inode: 1'\n"), 0o755)

	env := []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + fixtureRoot,
		"DATABASE_URL=" + dsn,
		"FAAS_APPS_ROOT=" + filepath.Join(fixtureRoot, "apps"),
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT=" + storageRoot,
		"FAAS_SIGN_KEY=" + signKeyPath,
		"FAAS_GUEST_INIT=" + guestInitPath,
		"FAAS_OCI_INSECURE=1",
		"FAAS_OCI_PULL_TIMEOUT_SECONDS=1",
		// A local 404 makes the normal registry-outage fallback deterministic;
		// imaged must accept the already-provisioned base.
		"FAAS_BUILDER_BASE_REF=" + strings.TrimPrefix(registry.URL, "http://") + "/builder-base@sha256:" + strings.Repeat("0", 64),
	}
	env = append(env, renderedImagedUnitEnvironment(t, unit, fixtureRoot, hostAgePath)...)

	proc := exec.Command(buildBootContractBinary(t, "cmd/imaged"), "--config", configPath)
	proc.Env = env
	var logs syncBuffer
	proc.Stdout = &logs
	proc.Stderr = &logs
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := proc.Start(); err != nil {
		t.Fatalf("start rendered imaged: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil && !t.Failed() {
				t.Errorf("imaged exited early: %v\n%s", err, logs.String())
			}
			return
		default:
		}
		_ = syscall.Kill(-proc.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-proc.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})

	waitBootReady(t, "http://"+controlAddr+"/readyz", 15*time.Second, &logs)
}

func buildBootContractBinary(t *testing.T, pkg string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", out, "./"+pkg)
	cmd.Dir = bootContractRepoRoot(t)
	var logs syncBuffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, logs.String())
	}
	return out
}

// TestBootContract_APIDAppErrorsDeploymentGuard pins the deployment half of
// production defect #1287. The runtime boot above proves apid can bind every
// listener; this assertion prevents the Ansible role from again accepting a
// fleet where compute nodes ship to AppErrors but apid has no matching bind.
// Reverting defect-fix commit 65005f8f4 removes this contract and fails here.
func TestBootContract_APIDAppErrorsDeploymentGuard(t *testing.T) {
	path := filepath.Join(bootContractRepoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read control-plane role: %v", err)
	}
	text := string(body)
	for _, required := range []string{
		"AppErrors listener must be set when any compute node ships to it",
		"faas_apid_app_errors_listen | default('') | length > 0",
		"faas_gatewayd_app_errors_target",
		"faas_app_errors_clients | length > 0",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("control-plane role is missing AppErrors deployment guard %q", required)
		}
	}
}

func writeRenderManifestWithDSN(t *testing.T, dsn string) string {
	t.Helper()
	const fixtureDSN = "postgres://faas@127.0.0.1:5432/faas"
	quoted := fmt.Sprintf("%q", dsn)
	body := strings.Replace(renderManifestYAML, fixtureDSN, quoted, 1)
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func renderedConfigPath(t *testing.T, unit daemonunit.Unit, etcDir, daemon string) string {
	t.Helper()
	fields := strings.Fields(unit.ExecStart)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "--config" {
			want := "/etc/faas/" + daemon + ".toml"
			if fields[i+1] != want {
				t.Fatalf("rendered config path = %q, want %s", fields[i+1], want)
			}
			return filepath.Join(etcDir, daemon+".toml")
		}
	}
	t.Fatalf("rendered ExecStart has no --config: %q", unit.ExecStart)
	return ""
}

func relocateRenderedMetricsAddr(t *testing.T, configPath, addr string) {
	t.Helper()
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read rendered config: %v", err)
	}
	lines := strings.Split(string(body), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "metrics_addr =") {
			if found {
				t.Fatal("rendered config has multiple metrics_addr entries")
			}
			lines[i] = "metrics_addr = " + fmt.Sprintf("%q", addr)
			found = true
		}
	}
	if !found {
		lines = append(lines, "metrics_addr = "+fmt.Sprintf("%q", addr))
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o640); err != nil {
		t.Fatalf("relocate rendered metrics_addr: %v", err)
	}
}

func renderedImagedUnitEnvironment(t *testing.T, unit daemonunit.Unit, root, hostAgePath string) []string {
	t.Helper()
	const wantEnvironmentFiles = "-/etc/faas/compute-db.env -/etc/faas/storage.env -/etc/faas/runtime-bases.env"
	if unit.EnvironmentFile != wantEnvironmentFiles {
		t.Fatalf("EnvironmentFile = %q, want %q", unit.EnvironmentFile, wantEnvironmentFiles)
	}

	runnerKeys := map[string]bool{
		"FAAS_FUNCTION_RUNNER_NODE22":       false,
		"FAAS_FUNCTION_RUNNER_NODE24":       false,
		"FAAS_FUNCTION_RUNNER_PYTHON312":    false,
		"FAAS_FUNCTION_RUNNER_PYTHON313":    false,
		"FAAS_FUNCTION_RUNNER_GO124":        false,
		"FAAS_FUNCTION_RUNNER_GO124_ALPINE": false,
	}
	dirReplacements := map[string]string{
		"TMPDIR":                 filepath.Join(root, "tmp"),
		"FAAS_BASE_STAGING_ROOT": filepath.Join(root, "base-staging"),
		"FAAS_BASE_EXTRACT_ROOT": filepath.Join(root, "base-extract"),
		"FAAS_BASE_TMP_ROOT":     filepath.Join(root, "base-tmp"),
	}
	var env []string
	for _, kv := range unit.Environment {
		value := kv.Value
		if dir, ok := dirReplacements[kv.Key]; ok {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("make %s fixture: %v", kv.Key, err)
			}
			value = dir
		}
		if kv.Key == "FAAS_HOST_AGE_IDENTITY_PATH" {
			value = hostAgePath
		}
		if _, ok := runnerKeys[kv.Key]; ok {
			runnerKeys[kv.Key] = true
			value = filepath.Join(root, "runners", strings.ToLower(strings.TrimPrefix(kv.Key, "FAAS_FUNCTION_RUNNER_")), "faas-runner")
			if err := os.MkdirAll(filepath.Dir(value), 0o755); err != nil {
				t.Fatalf("make %s fixture dir: %v", kv.Key, err)
			}
			writeBootKey(t, value, []byte("boot-contract runner\n"), 0o755)
		}
		if strings.Contains(value, "%d") {
			t.Fatalf("unresolved systemd credential path for %s=%s", kv.Key, value)
		}
		env = append(env, kv.Key+"="+value)
	}
	for key, seen := range runnerKeys {
		if !seen {
			t.Errorf("rendered imaged unit is missing %s", key)
		}
	}
	return env
}

func requiredUnitEnvironmentValue(t *testing.T, unit daemonunit.Unit, key string) string {
	t.Helper()
	var value string
	found := false
	for _, kv := range unit.Environment {
		if kv.Key != key {
			continue
		}
		if found {
			t.Fatalf("rendered unit has duplicate %s entries", key)
		}
		found = true
		value = strings.TrimSpace(kv.Value)
	}
	if !found || value == "" {
		t.Fatalf("rendered unit is missing non-empty %s", key)
	}
	return value
}

func seedBootContractBuilderBase(t *testing.T, storageRoot string, guestInit []byte) {
	t.Helper()
	baseKey := sched.BaseKeyForArch("builder", imagedpkg.BuilderArch())
	basePath := filepath.Join(storageRoot, filepath.FromSlash(baseKey))
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("make builder base fixture dir: %v", err)
	}
	if err := os.WriteFile(basePath, []byte("boot-contract ext4 placeholder\n"), 0o600); err != nil {
		t.Fatalf("write builder base fixture: %v", err)
	}
	digest := sha256.Sum256(guestInit)
	sidecar := "boot-contract\nguest-init-sha256=" + hex.EncodeToString(digest[:]) + "\n"
	digestPath := filepath.Join(storageRoot, filepath.FromSlash(sched.BaseDigestKeyForArch("builder", imagedpkg.BuilderArch())))
	if err := os.WriteFile(digestPath, []byte(sidecar), 0o600); err != nil {
		t.Fatalf("write builder base sidecar fixture: %v", err)
	}
}

func relocateRenderedDBURL(t *testing.T, configPath, dsn string) {
	t.Helper()
	const production = `db_url = "postgres:///faas?host=/run/postgresql&user=faas"`
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read rendered apid config: %v", err)
	}
	testDatabase := `db_url = ` + fmt.Sprintf("%q", dsn)
	switch {
	case strings.Count(string(body), production) == 1 && strings.Count(string(body), testDatabase) == 0:
		body = []byte(strings.Replace(string(body), production, testDatabase, 1))
	case strings.Count(string(body), production) == 0 && strings.Count(string(body), testDatabase) == 1:
		// Single-box manifests preserve their declared DSN; it already targets
		// the isolated schema and needs no relocation.
		return
	default:
		t.Fatalf("rendered apid config does not contain exactly one recognized db_url")
	}
	if err := os.WriteFile(configPath, body, 0o640); err != nil {
		t.Fatalf("relocate rendered apid db_url: %v", err)
	}
}

func renderedUnitEnvironment(t *testing.T, unit daemonunit.Unit, sessionKeyPath, hostAgePath, hostHMACPath, advisorySocket, root string) []string {
	t.Helper()
	const wantEnvironmentFiles = "/etc/faas/sealed.env -/etc/faas/storage.env"
	if unit.EnvironmentFile != wantEnvironmentFiles {
		t.Fatalf("EnvironmentFile = %q, want %q", unit.EnvironmentFile, wantEnvironmentFiles)
	}
	want := map[string]string{
		"FAAS_SESSION_KEY":            sessionKeyPath,
		"FAAS_HOST_AGE_IDENTITY_PATH": hostAgePath,
		"FAAS_HOST_HMAC_KEY_PATH":     hostHMACPath,
		"FAAS_LOG_ARCHIVE_CREDS_PATH": filepath.Join(root, "optional-archive-creds.json"),
		"FAAS_APID_ADVISORY_SOCK":     advisorySocket,
		"FAAS_STATUSPAGE_PATH":        filepath.Join(bootContractRepoRoot(t), "deploy", "statuspage", "index.html"),
	}
	seen := make(map[string]bool, len(want))
	var env []string
	for _, kv := range unit.Environment {
		value := kv.Value
		if replacement, ok := want[kv.Key]; ok {
			value = replacement
			seen[kv.Key] = true
		}
		if strings.Contains(value, "%d") {
			t.Fatalf("unresolved systemd credential path for %s=%s", kv.Key, value)
		}
		env = append(env, kv.Key+"="+value)
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("rendered unit is missing %s", key)
		}
	}
	return env
}

func waitBootReady(t *testing.T, url string, timeout time.Duration, logs *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("daemon did not become ready at %s within %s\n%s", url, timeout, logs.String())
		case <-ticker.C:
		}
	}
}

func assertUnixAccepts(t *testing.T, name, path string, logs *syncBuffer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s listener %s is not accepting: %v\n%s", name, path, lastErr, logs.String())
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random key: %v", err)
	}
	return b
}

func randomHexKey(t *testing.T) string {
	t.Helper()
	return hex.EncodeToString(randomBytes(t, 32))
}

func writeBootKey(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func bootContractRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate module root")
	return ""
}
