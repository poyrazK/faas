//go:build metal

// spec: §11

// secrets_env_metal_test.go — M5 acceptance for the G2 secrets wire
// (spec §11, §14).
//
// This is the only test that proves plaintext secrets reach a real
// firecracker guest: schedd → vmmd wire (Task #104) is half the story;
// the other half is "does vmmd's loopback-mount-write-umount actually
// deliver the bytes where guest-init will read them?". Unit tests in
// secrets_stage_test.go cover the KVM-free stage-of-bytes contract; this
// test covers the round-trip that the unit test cannot — a real VM, a
// real chroot mount, and a real workload receiving the decrypted values in
// its process environment.
//
// What's exercised end-to-end:
//
//   - Manager.ColdBoot with SealedEnvEntries set
//   - vmmd unseals each entry, merges into envelope, mounts drive1
//   - writes /etc/faas/secrets.env (JSON) with mode 0400
//   - guest-init reads the protected file and injects the values into the app
//   - a purpose-built probe app writes only its received values to tmpfs
//   - test GETs the probe result and asserts both values
//
// Environment: same as TestMetalHelloBoot (FAAS_TEST_KERNEL + a busybox
// ext4 rootfs). KVM + root required.

package fcvm

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalSecretsEnvReachesGuest is the M5 acceptance gate for G2
// secrets (spec §14). Cold-boots a busybox VM with two sealed env
// entries, then asserts that the guest workload received both decrypted
// values in its environment. The protected 0400 secrets file itself must not
// be exposed through the workload's HTTP server.
//
// Failure modes this test catches:
//
//   - the wire-shape redesign (Task #104) lost a field somewhere
//   - vmmd's loopback-mount path is wrong (wrong drive1 path or mode)
//   - Manager's unseal-and-merge has a key collision or skips entries
//   - guest-init does not merge the decrypted values into the app env
func TestMetalSecretsEnvReachesGuest(t *testing.T) {
	kernel, base, _ := metalImages(t)
	layer := ensureSecretsProbeLayer(t, t.TempDir())

	fcVer, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	m := NewManager(
		wire.ExecRunner{},
		newMetalVMM(t, 30*time.Second),
		Paths{Kernel: kernel},
		fcVer,
		slog.New(slog.NewTextHandler(testLogWriter{t}, nil)),
		nil,
	)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen host identity: %v", err)
	}
	m.SetHostIdentity(id)

	// Two rows — proves the merge loop handles multi-key fan-out and
	// that the JSON envelope keeps all keys distinct.
	want := secretbox.Envelope{
		"STRIPE_KEY": "sk_live_metal_test_" + time.Now().Format("150405.000"),
		"DB_URL":     "postgres://u:p@db:5432/app",
	}
	sealed := []SealedEnvEntry{
		{Key: "STRIPE_KEY", Ciphertext: mustSeal(t, id, secretbox.Envelope{"STRIPE_KEY": want["STRIPE_KEY"]})},
		{Key: "DB_URL", Ciphertext: mustSeal(t, id, secretbox.Envelope{"DB_URL": want["DB_URL"]})},
	}

	const instance = "m5-secrets"
	inst, err := m.ColdBoot(context.Background(), ColdBootRequest{
		Instance:         instance,
		Plan:             "hobby",
		BaseKey:          base,
		LayerKey:         layer,
		VcpuCount:        2,
		MemSizeMiB:       128,
		SealedEnvEntries: sealed,
	})
	if err != nil {
		t.Fatalf("cold boot with sealed env: %v", err)
	}
	t.Cleanup(func() {
		// Destroy even on assertion failure so we don't leak a chroot
		// (and so leakcheck.AssertZero downstream has a clean slate).
		_ = m.Destroy(context.Background(), instance)
	})

	// Read the tmpfs result produced by the app process from its environment.
	// Serving the root-owned secrets file directly would violate its 0400
	// contract and weaken the security property this test should preserve.
	url := "http://" + inst.Lease.HostIP.String() + ":8080/secrets-result"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status=%d body=%s", url, resp.StatusCode, body)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(got) != 2 || got[0] != want["STRIPE_KEY"] || got[1] != want["DB_URL"] {
		t.Fatalf("workload env result = %q, want both decrypted values", raw)
	}

	// Defensive: the on-disk plaintext must not contain a ciphertext
	// marker (regression guard for the "ship-the-wrong-shape" bug).
	if want := "AGE-"; containsSecretBytes(raw, want) {
		t.Errorf("secrets.env contains ciphertext marker %q — Manager shipped ciphertext to guest:\n%s",
			want, raw)
	}
}

// ensureSecretsProbeLayer creates a production-shaped drive1 image. The app
// writes the two injected variables to /tmp, which guest-init mounts as tmpfs,
// then serves that scratch directory. The encrypted fixture values are fake
// and exist only for this test run.
func ensureSecretsProbeLayer(t *testing.T, dir string) string {
	t.Helper()
	dst := filepath.Join(dir, "secrets-probe.ext4")
	skeleton := filepath.Join(dir, "skeleton")
	manifestDir := filepath.Join(skeleton, "upper", "etc", "faas")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("create secrets probe skeleton: %v", err)
	}
	const manifest = `{"entrypoint":["/bin/sh","-c","printf '%s\\n%s\\n' \"$STRIPE_KEY\" \"$DB_URL\" > /tmp/secrets-result && exec /bin/busybox httpd -f -p 8080 -h /tmp"],"port":8080}`
	if err := os.WriteFile(filepath.Join(manifestDir, "app.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write secrets probe manifest: %v", err)
	}
	if err := os.WriteFile(dst, nil, 0o644); err != nil {
		t.Fatalf("create secrets probe layer: %v", err)
	}
	if err := os.Truncate(dst, 16<<20); err != nil {
		t.Fatalf("size secrets probe layer: %v", err)
	}
	cmd := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", skeleton, "-L", "faas-secrets-probe", "-F", dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build secrets probe layer: %v (%s)", err, out)
	}
	return dst
}

// mustSeal seals env against id and fatals on error. Kept here so the
// test reads top-to-bottom without import noise.
func mustSeal(t *testing.T, id *age.X25519Identity, env secretbox.Envelope) []byte {
	t.Helper()
	blob, err := secretbox.Seal(id.Recipient(), env)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return blob
}

// contains is a tiny strings.Contains without the import dance —
// this test file is build-tagged `metal` and we don't want to drag in
// the strings package just for one substring check.
func containsSecretBytes(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
