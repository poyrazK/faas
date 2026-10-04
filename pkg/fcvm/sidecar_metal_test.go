//go:build metal

// adr: 069

// Sidecar metal tests (issue #463 / ADR-069 / PR-B).
//
// These tests run a real jailed firecracker with the PR-B
// N+1 drive topology (drive0 = base, drive1 = main RW, drive2
// = sidecar-0 RO, drive3 = sidecar-1 RO) and assert:
//
//  1. TestMetalSidecarBoot — guest-init's runWorkloads orchestrator
//     discovers the deployment-level roster on drive1 and
//     fork/execs the main workload + a single metrics sidecar
//     in parallel under per-workload Supervisors.
//  2. TestMetalSidecarPortReachable — the sidecar's TCP listener
//     (busybox httpd on a customer-pinned port) is reachable from
//     inside the netns via the host IP. AC #2 contract: a sidecar
//     runs alongside the main workload, reachable on a per-app
//     port inside the netns.
//  3. TestMetalTwoSidecarsColdBoot — multiple sidecars in the same
//     deployment cold-boot successfully (PR-B review finding #6
//     renamed it from 'TestMetalTwoSidecarsDistinctUUID' because
//     the prior name implied a UUID assertion the body never
//     wired — see cmd/e2e/v6_distinct_uuid_e2e_test.go for the
//     actual UUID gate).
//  4. TestMetalSidecarOOMIsolation — a sidecar that exceeds its
//     cgroup memory.max dies WITHOUT killing the main workload.
//     This is the AC #4 acceptance gate: a runaway sidecar must
//     not take down the customer's app.
//
// All tests share ensureSidecarExt4 which mirrors ensureBusyboxExt4
// but builds a per-sidecar ext4 with /usr/local/bin/start.sh as
// the canonical sidecar entrypoint (PR-A contract).
//
// The metal suite runs as root on a real EX44 or via Lima nested
// KVM. Without /dev/kvm the file compiles but the test binary
// exits at TestMain.

package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	faasnetns "github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// ensureSidecarExt4 returns the path to a sidecar ext4 image,
// creating one in dir if none exists. Mirrors ensureBusyboxExt4's
// fixture/build-fallback pattern but the sidecar ext4 ships:
//
//	/usr/local/bin/start.sh  (the canonical sidecar entrypoint)
//	/etc/sidecar/start.sh    (operator-visibility alias)
//
// The start.sh exec's `busybox httpd -f -p <port>` so the test
// can verify the sidecar listens on the customer-pinned port
// without needing a real OCI image. A future PR-C wires the
// customer-supplied cmd field; today every sidecar image ships
// the canonical start.sh per imaged's stamp during buildSidecarLayer.
//
// port is hardcoded to the busybox httpd listen port; the
// customer-pinned port comes from WorkloadSpec.Port on the wake
// wire and is the value the test's main workload uses to dial.
func ensureSidecarExt4(t *testing.T, dir, name string, port int) string {
	t.Helper()
	dst := filepath.Join(dir, fmt.Sprintf("sidecar-%s-%d.ext4", name, port))
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	if err := buildSidecarExt4(dst, name, port); err != nil {
		t.Fatalf("build sidecar ext4: %v", err)
	}
	return dst
}

// buildSidecarExt4 makes a tiny optimized sidecar ext4 image. The image tree
// lives below /upper, matching rootfs.Builder's production artifact layout.
// Its busybox httpd entrypoint lives at /usr/local/bin/start.sh — the
// canonical sidecar image convention. The mkfs call is the same
// journal-less recipe as buildBusyboxExt4 because guest-init mounts the
// sidecar drive read-only and chroots the workload into its /upper tree.
func buildSidecarExt4(dst, name string, port int) error {
	return buildSidecarExt4WithReload(dst, name, port, false)
}

func buildSidecarExt4WithReload(dst, name string, port int, reload bool) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return fmt.Errorf("busybox not on PATH: %w", err)
	}

	work, err := os.MkdirTemp("", "sidecar-skel-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	for _, sub := range []string{"upper/bin", "upper/usr/local/bin", "upper/etc/sidecar", "upper/dev", "upper/proc", "upper/sys", "upper/tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}

	// Copy busybox into the skeleton's /usr/local/bin so the
	// start.sh exec can find it without a symlink dance.
	if err := bbCopyFile(bb, filepath.Join(work, "upper/usr/local/bin/busybox")); err != nil {
		return err
	}
	if err := os.Symlink("/usr/local/bin/busybox", filepath.Join(work, "upper/bin/sh")); err != nil {
		return err
	}
	start := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/busybox httpd -f -p %d -h /\n", port)
	manifest := api.AppManifest{Entrypoint: []string{"/usr/local/bin/start.sh"}, WorkingDir: "/", User: "0", Port: port}
	if reload {
		manifest.User, manifest.SecretReloadSignal = "reload-worker", "SIGHUP"
		manifest.SecretReloadReadiness = true
		start = fmt.Sprintf("#!/bin/sh\n"+
			"if [ ! -f /tmp/reload-started ]; then printf '%%s' \"$FAAS_SECRETS_RELOAD_GENERATION\" > /tmp/first-generation; printf 'first' > /tmp/reload-started; exit 17; fi\n"+
			strings.ReplaceAll(metalSecretProjectionWait("/usr/local/bin/busybox"), "%", "%%")+
			"printf '%%s|%%s|%%s|%%s|restarted' \"$DATABASE_URL\" \"$TOKEN\" \"$(/usr/local/bin/busybox cat \"$FAAS_SECRETS_REVISION_FILE\")\" \"$(/usr/local/bin/busybox id -u)\" > /tmp/index.html\n"+
			"/usr/local/bin/busybox cat \"$FAAS_SECRETS_SNAPSHOT_FILE\" > /tmp/snapshot.json\n"+
			"printf 'ready\\n' > \"$FAAS_SECRETS_RELOAD_READY_FILE\"; /usr/local/bin/busybox cat \"$FAAS_SECRETS_RELOAD_READY_FILE\" > /tmp/reload-ready\n"+
			"/usr/local/bin/busybox mkdir -p /tmp/cgi-bin; /usr/local/bin/busybox cp /usr/local/bin/ack-old /usr/local/bin/ack-current /tmp/cgi-bin/\n"+
			"exec /usr/local/bin/busybox httpd -f -p %d -h /tmp\n", port)
		if err := os.MkdirAll(filepath.Join(work, "upper/tmp/cgi-bin"), 0755); err != nil {
			return err
		}
		for name, generationFile := range map[string]string{"ack-old": "/tmp/first-generation", "ack-current": "/tmp/current-generation"} {
			if err := os.WriteFile(filepath.Join(work, "upper/usr/local/bin", name), []byte(metalSecretAckCGI("/usr/local/bin/busybox", generationFile)), 0755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(work, "upper/etc/passwd"), []byte("reload-worker:x:1001:1001::/:/bin/sh\n"), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, "upper/etc/group"), []byte("reload-worker:x:1001:\n"), 0644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(work, "upper/usr/local/bin/start.sh"), []byte(start), 0o755); err != nil {
		return err
	}
	// Operator-visibility alias used by `cat /etc/sidecar/start.sh`
	// inside the guest to confirm the sidecar is the right one.
	if err := os.WriteFile(filepath.Join(work, "upper/etc/sidecar/start.sh"), []byte(start), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "upper/index.html"), []byte("<h1>sidecar-ready</h1>\n"), 0o644); err != nil {
		return err
	}
	if err := rootfs.InjectWorkloadManifest(filepath.Join(work, "upper"), name, manifest); err != nil {
		return fmt.Errorf("inject workload manifest: %w", err)
	}

	// Pre-size the file (modern e2fsprogs refuses zero-block input).
	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}

	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-sidecar", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w", err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		return fmt.Errorf("chmod sidecar ext4: %w", err)
	}
	return nil
}

func sidecarHTTPInNetNS(ctx context.Context, namespace string, port int, requestPath, output string) ([]byte, error) {
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		return nil, fmt.Errorf("busybox not on PATH: %w", err)
	}
	url := fmt.Sprintf("http://%s:%d%s", faasnetns.GuestIP, port, requestPath)
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", namespace, busybox, "wget", "-q", "-O", output, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("GET %s in %s: %w: %s", url, namespace, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func waitForSidecarHTTP(ctx context.Context, namespace string, port int, requestPath string) ([]byte, error) {
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		body, err := sidecarHTTPInNetNS(attemptCtx, namespace, port, requestPath, "-")
		cancel()
		if err == nil {
			return body, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for sidecar :%d%s in %s: %w (last result: %v)", port, requestPath, namespace, ctx.Err(), lastErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestMetalSidecarBoot is the M7 PR-B headline test (AC #1 +
// AC #2). It boots a guest with one sidecar and asserts:
//
//   - The wake returns (no panic in the orchestrator).
//   - The guest supervisor starts the main and sidecar workloads.
//   - The guest-init /etc/faas/workloads.json was stamped on drive1
//     by StageWorkloadRoster and survives pivot_root.
//
// The HTTP probe against the main workload's :8080 is the
// waitReady handshake — proving the supervisor reaches the
// RUNNING state — and the absence of any error path inside
// the orchestrator is the AC #1 acceptance.
//
// AC #4 (OOM isolation) and AC #2 (sidecar port reachable) get
// their own tests below; this one is the "did it boot at all"
// smoke gate.
func TestMetalSidecarBoot(t *testing.T) {
	metalSidecarBoot(t, false)
}

// ADR-503: the guest must boot both supervisors when main opts into reload.
// The prior guest startup rejected this image/roster combination entirely.
func TestMetalMainSecretReloadWithSidecar(t *testing.T) {
	metalSidecarBoot(t, true)
}

// ADR-504: an opted-in named sidecar user can read its prepared projection
// after the essential supervisor restarts its first deliberately failed start.
func TestMetalSidecarSecretReloadNamedUserRestart(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	ledger := installMetalSecretGenerationReceiver(t, m)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	m.SetHostIdentity(id)
	sidecar := filepath.Join(t.TempDir(), "reload-sidecar.ext4")
	if err := buildSidecarExt4WithReload(sidecar, "reload", 9093, true); err != nil {
		t.Fatal(err)
	}
	sealed := make([]SealedEnvEntry, 0, 2)
	for _, key := range []string{"DATABASE_URL", "TOKEN"} {
		value := "reload-" + key
		ciphertext, err := secretbox.Seal(id.Recipient(), secretbox.Envelope{key: value})
		if err != nil {
			t.Fatal(err)
		}
		sealed = append(sealed, SealedEnvEntry{Key: key, Ciphertext: ciphertext})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const instance = "sidecar-secret-restart"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "hobby", BaseKey: base, LayerKey: layer,
		VcpuCount: 2, MemSizeMiB: 256, Port: 8080,
		Sidecars: []WorkloadSpec{{Name: "reload", Type: "sidecar", StorageKey: sidecar,
			DriveID: "layer-sidecar-0", RamMB: 64, Port: 9093, Essential: true, SealedSecrets: sealed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	readyCtx, readyCancel := context.WithTimeout(ctx, 15*time.Second)
	body, err := waitForSidecarHTTP(readyCtx, inst.Lease.Netns, 9093, "/")
	readyCancel()
	if err != nil || string(body) != "reload-DATABASE_URL|reload-TOKEN|"+metalSecretRevision+"|1001|restarted" {
		t.Fatalf("named sidecar restart env/projection = %q %v", body, err)
	}
	marker, markerErr := waitForSidecarHTTP(ctx, inst.Lease.Netns, 9093, "/reload-ready")
	if markerErr != nil || string(marker) != "ready\n" {
		t.Fatalf("named-user readiness marker=%q %v", marker, markerErr)
	}
	assertMetalSecretSnapshot(t, ctx, inst.Lease.Netns, 9093, map[string]string{"DATABASE_URL": "reload-DATABASE_URL", "TOKEN": "reload-TOKEN"})
	body, err = waitForSidecarHTTP(ctx, inst.Lease.Netns, 9093, "/cgi-bin/ack-old")
	assertMetalSecretAckResult(t, body, err, false)
	body, err = waitForSidecarHTTP(ctx, inst.Lease.Netns, 9093, "/cgi-bin/ack-current")
	assertMetalSecretAckResult(t, body, err, true)
	ledger.assertAck(t, instance, "reload", true)
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}

func metalSidecarBoot(t *testing.T, mainReload bool) {
	t.Helper()
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	var ledger *metalSecretGenerationLedger
	if mainReload {
		ledger = installMetalSecretGenerationReceiver(t, m)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureSidecarExt4(t, tmp, "metrics", 9090)
	if mainReload {
		layer = mainSecretReloadMetalLayer(t, layer)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const (
		instance = "prb-sidecar-1"
		mainPort = 8080
	)
	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       mainPort,
		Sidecars: []WorkloadSpec{
			{
				Name:       "metrics",
				Type:       "sidecar",
				StorageKey: sidecar,
				DriveID:    "layer-sidecar-0",
				RamMB:      64,
				Port:       9090,
				Essential:  true,
			},
		},
	})
	if err != nil {
		t.Fatalf("PR-B sidecar cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	// Probe the main workload's :8080 listener (the waitReady
	// handshake already did this once; the second probe here
	// is the AC #1 surface — the boot path must end at a
	// running supervisor, not a crash-looped one).
	inst, ok := m.LiveInstances()[instance]
	if !ok {
		t.Fatalf("instance %q not in live map", instance)
	}
	url := fmt.Sprintf("http://%s:8080/", inst.Lease.HostIP.String())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("main :8080 returned server error %d: %s", resp.StatusCode, body)
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = waitForSidecarHTTP(readyCtx, inst.Lease.Netns, 9090, "/")
	readyCancel()
	if err != nil {
		t.Fatalf("sidecar readiness: %v", err)
	}

	if mainReload {
		assertMetalSecretSnapshot(t, ctx, inst.Lease.Netns, 8080, map[string]string{})
		body, err := waitForSidecarHTTP(ctx, inst.Lease.Netns, 8080, "/cgi-bin/ack-current")
		assertMetalSecretAckResult(t, body, err, true)
		ledger.assertAck(t, instance, "", false)
	}

	// Tear down and verify the per-instance host resources are gone.
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

func mainSecretReloadMetalLayer(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	layer, mount := filepath.Join(dir, "main-reload.ext4"), filepath.Join(dir, "mnt")
	if err := copyFile(source, layer); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", "-o", "loop", layer, mount).CombinedOutput(); err != nil {
		t.Fatalf("mount reload fixture: %v %s", err, out)
	}
	defer func() {
		if out, err := exec.Command("umount", mount).CombinedOutput(); err != nil {
			t.Errorf("unmount reload fixture: %v %s", err, out)
		}
	}()
	manifestPath, err := stagedDrivePath(mount, "upper/"+strings.TrimPrefix(api.AppManifestPath, "/"))
	if err != nil {
		t.Fatal(err)
	}
	root, relative, err := openDriveRoot(mount, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	body, err := root.ReadFile(relative)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := api.ReadManifest(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	manifest.SecretReloadSignal = "SIGHUP"
	manifest.Entrypoint = []string{"/bin/sh", "-c", metalSecretProjectionWait("/bin/busybox") + `/bin/busybox mkdir -p /tmp/cgi-bin; /bin/busybox cp /usr/local/bin/ack-current /tmp/cgi-bin/; cat "$FAAS_SECRETS_SNAPSHOT_FILE" > /tmp/snapshot.json; printf 'ready' > /tmp/index.html; exec /bin/busybox httpd -f -p 8080 -h /tmp`}
	if manifest.StopSignal == "SIGHUP" {
		manifest.SecretReloadSignal = "SIGUSR1"
	}
	if err := writeDriveFile(mount, "upper/usr/local/bin/ack-current", []byte(metalSecretAckCGI("/bin/busybox", "/tmp/current-generation")), 0755, "generation ACK fixture"); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := api.WriteManifest(&encoded, manifest); err != nil {
		t.Fatal(err)
	}
	if err := writeDriveFile(mount, "upper/"+strings.TrimPrefix(api.AppManifestPath, "/"), encoded.Bytes(), 0o644, "main reload fixture"); err != nil {
		t.Fatal(err)
	}
	return layer
}

// TestMetalSidecarPortReachable covers AC #2: a sidecar runs
// alongside the main workload, reachable on a customer-pinned
// port inside the netns. The test enters the instance network namespace and
// dials the guest address directly, matching how the main workload reaches an
// internal sidecar.
//
// The gateway's per-instance portnorm ladder publishes only the main port;
// sidecar ports remain internal to the deployment. This
// proves the underlying guest-init + isolated-root + cgroup wiring
// without depending on the gateway-side portnorm that PR-C
// lands separately.
func TestMetalSidecarPortReachable(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureSidecarExt4(t, tmp, "metrics", 9091)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const instance = "prb-sidecar-port-1"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "metrics", Type: "sidecar", StorageKey: sidecar, DriveID: "layer-sidecar-0", RamMB: 64, Port: 9091, Essential: true},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	body, err := waitForSidecarHTTP(readyCtx, inst.Lease.Netns, 9091, "/")
	readyCancel()
	if err != nil {
		t.Fatalf("sidecar :9091 readiness: %v", err)
	}
	if !strings.Contains(string(body), "sidecar-ready") {
		t.Fatalf("sidecar :9091 body = %q, want readiness fixture", body)
	}

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalTwoSidecarsColdBoot pins multi-sidecar cold boot
// (PR-B review finding #6). It boots a guest with TWO sidecars
// on the same deployment (below the expanded cap of 5) and asserts
// the cold-boot path doesn't panic — that's the load-bearing
// surface for the per-workload cgroup scopes and the N+1 drive
// topology. The previous name 'TestMetalTwoSidecarsDistinctUUID'
// implied a UUID assertion that was never wired (the body sets
// inst then ignores it via '_ = inst'); the rename surfaces the
// real contract. The distinct-UUID gate is in
// cmd/e2e/v6_distinct_uuid_e2e_test.go, where the vsock probe
// can read /proc/sys/kernel/random/uuid from inside the guest.
func TestMetalTwoSidecarsColdBoot(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sc0 := ensureSidecarExt4(t, tmp, "metrics", 9100)
	sc1 := ensureSidecarExt4(t, tmp, "logger", 9101)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const instance = "prb-sidecar-2"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "metrics", Type: "sidecar", StorageKey: sc0, DriveID: "layer-sidecar-0", RamMB: 64, Port: 9100, Essential: true},
			{Name: "logger", Type: "sidecar", StorageKey: sc1, DriveID: "layer-sidecar-1", RamMB: 32, Port: 9101, Essential: false},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	for _, port := range []int{9100, 9101} {
		readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
		body, readyErr := waitForSidecarHTTP(readyCtx, inst.Lease.Netns, port, "/")
		readyCancel()
		if readyErr != nil {
			t.Fatalf("sidecar :%d readiness: %v", port, readyErr)
		}
		if !strings.Contains(string(body), "sidecar-ready") {
			t.Fatalf("sidecar :%d body = %q, want readiness fixture", port, body)
		}
	}
	// UUID readback lives in cmd/e2e/v6_distinct_uuid_e2e_test.go.

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalSidecarOOMIsolation requires an anonymous-memory allocator in
// the companion leaf to die with SIGKILL under its 16 MiB limit, then checks
// that the main workload still serves requests. A CGI wrapper survives the
// allocator and reports its exit status, so an HTTP failure or reclaimable
// file-cache pressure cannot silently satisfy the gate.
// The test requires native KVM and cgroup v2; qualification rejects skips.
func TestMetalSidecarOOMIsolation(t *testing.T) {
	// Pre-flight: cgroup v2 + the per-workload path. The
	// six-guarded skip mirrors the §11 production posture
	// (cgroups v2 is required for firecracker snapshot
	// restore and the per-workload memcg isolation; the
	// README / CLAUDE.md "cgroup v2 only" rule is enforced
	// here, not at boot).
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skipf("cgroup v2 unavailable on this host (%v); AC #4 metal gate requires v2", err)
	}
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureOOMSidecarExt4(t, tmp, "stress", 9092)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const (
		instance     = "prc-sidecar-oom-1"
		mainPort     = 8080
		sidecarPort  = 9092
		sidecarRamMB = 16
	)
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       mainPort,
		Sidecars: []WorkloadSpec{
			{
				Name:       "stress",
				Type:       "sidecar",
				StorageKey: sidecar,
				DriveID:    "layer-sidecar-0",
				RamMB:      sidecarRamMB,
				Port:       sidecarPort,
				Essential:  false,
			},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort destroy — the sidecar OOM may leave
		// the guest in a half-reaped state, but the host's
		// netns/cgroup cleanup is idempotent.
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	// Step 1: probe main workload must succeed (precondition).
	mainURL := fmt.Sprintf("http://%s:%d/", inst.Lease.HostIP.String(), mainPort)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(mainURL)
	if err != nil {
		t.Fatalf("main GET %s: %v", mainURL, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("main :8080 (pre) = %d, want a live HTTP response", resp.StatusCode)
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	readyBody, err := waitForSidecarHTTP(readyCtx, inst.Lease.Netns, sidecarPort, "/")
	readyCancel()
	if err != nil {
		t.Fatalf("sidecar pre-OOM readiness: %v", err)
	}
	if !strings.Contains(string(readyBody), "oom-test") {
		t.Fatalf("sidecar :%d body = %q, want OOM fixture", sidecarPort, readyBody)
	}

	// Allocate and touch anonymous memory in a CGI child. Unlike file cache,
	// this cannot be reclaimed to satisfy the leaf's 16 MiB memory.max.
	// Require the shell's SIGKILL exit status; transport errors alone do not
	// prove that memory pressure killed a process.
	oomCtx, oomCancel := context.WithTimeout(ctx, 15*time.Second)
	killedBody, err := sidecarHTTPInNetNS(oomCtx, inst.Lease.Netns, sidecarPort, "/cgi-bin/stress", "-")
	oomCancel()
	if err != nil {
		t.Fatalf("memory stress response: %v", err)
	}
	if !strings.Contains(string(killedBody), "stress_exit=137") {
		t.Fatalf("anonymous memory stress did not observe SIGKILL: %q", killedBody)
	}

	// Step 3: probe main workload again. AC #4 acceptance:
	// the main workload MUST still answer 2xx after the
	// sidecar OOMs. A regression that lets the memcg OOM
	// propagate to the parent scope would 503 here.
	// Allow a brief settle for the OOM-killer to reap +
	// the postmortem to settle.
	time.Sleep(500 * time.Millisecond)
	resp2, err := client.Get(mainURL)
	if err != nil {
		t.Fatalf("main GET (post-OOM) %s: %v", mainURL, err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode < http.StatusOK || resp2.StatusCode >= http.StatusMultipleChoices {
		t.Errorf("main :8080 (post-OOM) = %d, want a live HTTP response (AC #4 violated: sidecar OOM propagated to main workload)",
			resp2.StatusCode)
	}
}

// ensureOOMSidecarExt4 builds a busybox HTTP fixture with a CGI endpoint
// that grows a retained anonymous string beyond the companion's RAM limit.
// Its wrapper reports the allocator's exit status after the kernel kills it.
// Reclaimable page-cache pressure is insufficient evidence of OOM isolation.
func ensureOOMSidecarExt4(t *testing.T, dir, name string, port int) string {
	t.Helper()
	dst := filepath.Join(dir, fmt.Sprintf("sidecar-oom-%s-%d.ext4", name, port))
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("busybox not on PATH: %v", err)
	}

	work, err := os.MkdirTemp("", "sidecar-oom-skel-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(work)

	for _, sub := range []string{"upper/bin", "upper/usr/local/bin", "upper/etc/sidecar", "upper/var/log", "upper/dev", "upper/proc", "upper/sys", "upper/tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := bbCopyFile(bb, filepath.Join(work, "upper/usr/local/bin/busybox")); err != nil {
		t.Fatalf("copy busybox: %v", err)
	}
	if err := os.Symlink("/usr/local/bin/busybox", filepath.Join(work, "upper/bin/sh")); err != nil {
		t.Fatalf("symlink sh: %v", err)
	}
	cgiDir := filepath.Join(work, "upper/var/log/cgi-bin")
	if err := os.MkdirAll(cgiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stress := `#!/bin/sh
printf 'Content-Type: text/plain\r\n\r\n'
/usr/local/bin/busybox awk 'BEGIN { s="xxxxxxxxxxxxxxxx"; for (i=0; i<23; i++) s=s s; print length(s) }'
status=$?
printf 'stress_exit=%s\n' "$status"
`
	if err := os.WriteFile(filepath.Join(cgiDir, "stress"), []byte(stress), 0o755); err != nil {
		t.Fatal(err)
	}

	start := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/busybox httpd -f -p %d -h /var/log\n", port)
	if err := os.WriteFile(filepath.Join(work, "upper/usr/local/bin/start.sh"), []byte(start), 0o755); err != nil {
		t.Fatalf("write start.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "upper/etc/sidecar/start.sh"), []byte(start), 0o644); err != nil {
		t.Fatalf("write alias: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "upper/var/log/index.html"), []byte("<h1>oom-test</h1>\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := rootfs.InjectWorkloadManifest(filepath.Join(work, "upper"), name, api.AppManifest{
		Entrypoint: []string{"/usr/local/bin/start.sh"},
		WorkingDir: "/",
		User:       "0",
		Port:       port,
	}); err != nil {
		t.Fatalf("inject workload manifest: %v", err)
	}

	if f, err := os.Create(dst); err != nil {
		t.Fatalf("create ext4: %v", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		t.Fatalf("size ext4: %v", err)
	} else if err := f.Close(); err != nil {
		t.Fatalf("close ext4: %v", err)
	}
	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-sidecar-oom", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mkfs.ext4: %v", err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		t.Fatalf("chmod ext4: %v", err)
	}
	return dst
}

// ADR-505: both main and isolated named-user sidecars receive readable envelopes.
func assertMetalSecretSnapshot(t *testing.T, ctx context.Context, namespace string, port int, expected map[string]string) {
	t.Helper()
	body, err := waitForSidecarHTTP(ctx, namespace, port, "/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Revision string            `json:"revision"`
		Secrets  map[string]string `json:"secrets"`
	}
	if json.Unmarshal(body, &snapshot) != nil || snapshot.Revision != metalSecretRevision || !reflect.DeepEqual(snapshot.Secrets, expected) {
		t.Fatal("guest snapshot did not preserve its prepared revision and authorized values")
	}
}
