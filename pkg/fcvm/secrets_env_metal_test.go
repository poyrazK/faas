//go:build metal

// secrets_env_metal_test.go — M5 acceptance for the G2 secrets wire
// (spec §11, §14).
//
// This is the only test that proves plaintext secrets reach a real
// firecracker guest: schedd → vmmd wire (Task #104) is half the story;
// the other half is "does vmmd's loopback-mount-write-umount actually
// deliver the bytes where guest-init will read them?". Unit tests in
// secrets_stage_test.go cover the KVM-free stage-of-bytes contract; this
// test covers the round-trip that the unit test cannot — a real VM, a
// real chroot mount, a real httpd serving /etc/faas/secrets.env.
//
// What's exercised end-to-end:
//
//   - Manager.ColdBoot with SealedEnvEntries set
//   - vmmd unseals each entry, merges into envelope, mounts drive1
//   - writes /etc/faas/secrets.env (JSON) with mode 0400
//   - guest boots, busybox httpd serves the file
//   - test GETs the URL, asserts plaintext key/values
//
// Environment: same as TestMetalHelloBoot (FAAS_TEST_KERNEL + a busybox
// ext4 rootfs). KVM + root required.

package fcvm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalSecretsEnvReachesGuest is the M5 acceptance gate for G2
// secrets (spec §14). Cold-boots a busybox VM with two sealed env
// entries, GETs /etc/faas/secrets.env from the guest's HTTP server,
// and asserts the unsealed plaintext key/values match what we sealed.
//
// Failure modes this test catches:
//
//   - the wire-shape redesign (Task #104) lost a field somewhere
//   - vmmd's loopback-mount path is wrong (wrong drive1 path, no
//     chmod, wrong perms for httpd to read)
//   - Manager's unseal-and-merge has a key collision or skips entries
//   - the JSON envelope shape on disk differs from what guest-init
//     would read (regression that would break the real guest too)
func TestMetalSecretsEnvReachesGuest(t *testing.T) {
	kernel, _, _ := metalImages(t)
	tmp := t.TempDir()
	rootfs := ensureBusyboxExt4(t, tmp)

	fcVer, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	m := NewManager(
		wire.ExecRunner{},
		NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel},
		fcVer,
		slog.New(slog.NewTextHandler(testLogWriter{t}, nil)),
		nil,
	)
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
		BasePath:         rootfs,
		LayerPath:        rootfs,
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

	// waitReady inside Boot already proved the kernel is up; we now
	// verify the secrets file is reachable over HTTP. httpd serves
	// the root filesystem, so /etc/faas/secrets.env is at the URL below.
	url := "http://" + inst.Lease.HostIP.String() + ":8080/etc/faas/secrets.env"
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

	// The on-disk shape is canonical JSON (Manager re-marshals after
	// merge); decode and verify plaintext. This is the exact contract
	// guest-init depends on at runtime.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got secretbox.Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode envelope (body=%s): %v", raw, err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("secrets.env[%q] = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("secrets.env has %d keys, want %d (got=%+v)", len(got), len(want), got)
	}

	// Defensive: the on-disk plaintext must not contain a ciphertext
	// marker (regression guard for the "ship-the-wrong-shape" bug).
	if want := "AGE-"; contains(raw, want) {
		t.Errorf("secrets.env contains ciphertext marker %q — Manager shipped ciphertext to guest:\n%s",
			want, raw)
	}
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
func contains(haystack []byte, needle string) bool {
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
