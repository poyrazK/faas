package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/renderer"
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
	configPath := renderedConfigPath(t, unit, etcDir)
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
	writeBootKey(t, hostAgePath, []byte(identity.String()+"\n"), 0o400)

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

func renderedConfigPath(t *testing.T, unit daemonunit.Unit, etcDir string) string {
	t.Helper()
	fields := strings.Fields(unit.ExecStart)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "--config" {
			if fields[i+1] != "/etc/faas/apid.toml" {
				t.Fatalf("rendered config path = %q, want /etc/faas/apid.toml", fields[i+1])
			}
			return filepath.Join(etcDir, "apid.toml")
		}
	}
	t.Fatalf("rendered ExecStart has no --config: %q", unit.ExecStart)
	return ""
}

func relocateRenderedDBURL(t *testing.T, configPath, dsn string) {
	t.Helper()
	const production = `db_url = "postgres:///faas?host=/run/postgresql&user=faas"`
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read rendered apid config: %v", err)
	}
	if strings.Count(string(body), production) != 1 {
		t.Fatalf("rendered apid config does not contain exactly one production-local db_url")
	}
	body = []byte(strings.Replace(string(body), production, `db_url = `+fmt.Sprintf("%q", dsn), 1))
	if err := os.WriteFile(configPath, body, 0o640); err != nil {
		t.Fatalf("relocate rendered apid db_url: %v", err)
	}
}

func renderedUnitEnvironment(t *testing.T, unit daemonunit.Unit, sessionKeyPath, hostAgePath, hostHMACPath, advisorySocket, root string) []string {
	t.Helper()
	if unit.EnvironmentFile != "/etc/faas/sealed.env" {
		t.Fatalf("EnvironmentFile = %q, want /etc/faas/sealed.env", unit.EnvironmentFile)
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
			t.Fatalf("apid did not become ready at %s within %s\n%s", url, timeout, logs.String())
		case <-ticker.C:
		}
	}
}

func assertUnixAccepts(t *testing.T, name, path string, logs *syncBuffer) {
	t.Helper()
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("%s listener %s is not accepting: %v\n%s", name, path, err, logs.String())
	}
	_ = conn.Close()
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
