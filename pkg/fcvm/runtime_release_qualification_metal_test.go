//go:build metal

package fcvm

// adr: 600

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/runtimequalification"
)

// This test intentionally has no synthetic image fallback. A missing fixture
// can skip a generic metal run, but any skip is rejected by the signed importer.
func TestMetalRuntimeReleaseColdBootReady(t *testing.T) {
	path := os.Getenv("FAAS_RUNTIME_QUALIFICATION_FIXTURE")
	if path == "" {
		t.Skip("exact published runtime qualification fixture not selected")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("qualification requires native Linux amd64 KVM as root")
	}
	//nolint:forbidigo,gosec // Private operator-selected fixture on a dedicated acceptance host; bounded before decoding.
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := runtimequalification.ReadBounded(file, api.RuntimeQualificationReportMaxBytes)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	f, err := runtimequalification.DecodeFixture(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	host, boot := qualificationNativeHost(t, ctx)
	if host != f.HostID || qualificationSourceCommit(t, ctx) != f.SourceCommit {
		t.Fatal("native host or checkout differs from selected attempt")
	}
	kernel, base, layer := metalImages(t)
	baseInfo, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	layerInfo, err := os.Stat(layer)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(baseInfo, layerInfo) {
		t.Fatal("qualification requires distinct base and layer drives")
	}
	fc, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		t.Fatal(err)
	}
	fc, err = filepath.EvalSymlinks(fc)
	if err != nil {
		t.Fatal(err)
	}
	measured := map[string]string{kernel: f.KernelSHA256, base: f.Target.BaseSHA256, layer: f.LayerSHA256, fc: f.FirecrackerSHA256}
	qualificationAssets(t, measured)
	initSHA := qualificationGuestInit(t, ctx, base)
	if initSHA != f.Target.GuestInitSHA256 {
		t.Fatal("guest-init in exact base differs from catalogue")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	m := newMetalManager(t, kernel)
	id := uuid.NewString()
	retired := false
	t.Cleanup(func() {
		if !retired {
			cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := m.Destroy(cleanup, id); err != nil {
				t.Logf("qualification cleanup: %v", err)
			}
		}
	})
	inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: id, Plan: api.PlanPro, BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, Port: 8080, HealthcheckPath: "/"})
	if err != nil {
		t.Fatal("exact runtime cold boot:", err)
	}
	if inst.Method != WakeColdBoot || m.LiveCount() != 1 {
		t.Fatal("fixture did not start with a fresh cold boot")
	}
	qualificationHTTP(t, ctx, net.JoinHostPort(inst.Lease.HostIP.String(), "8080"), "gregale-runtime-qualified:"+f.Target.Runtime)
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatal("retire qualified fixture:", err)
	}
	retired = true
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("qualification fixture did not retire all leases")
	}
	leakcheck.AssertZero(t)
	qualificationAssets(t, measured)
	n := runtimequalification.Observation{RunID: f.RunID, ReleaseID: f.Target.ID, HostID: host, KernelBootID: boot, SourceCommit: f.SourceCommit, DeploymentID: f.DeploymentID, LayerKey: f.LayerKey,
		BaseSHA256: f.Target.BaseSHA256, GuestInitSHA256: initSHA, LayerSHA256: f.LayerSHA256, KernelSHA256: f.KernelSHA256, FirecrackerSHA256: f.FirecrackerSHA256,
		OS: runtime.GOOS, Architecture: runtime.GOARCH, Virtualization: "none", KVM: true, ColdBoot: true, Ready: true, Retired: true}
	observation, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s%s", runtimequalification.ObservationMarker, observation)
}

func qualificationNativeHost(t *testing.T, ctx context.Context) (host, boot string) {
	t.Helper()
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatal("designated acceptance host marker absent:", err)
	}
	kvmInfo, err := os.Stat("/dev/kvm")
	if err != nil || kvmInfo.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("native KVM device absent:", err)
	}
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		t.Fatal("native KVM unavailable:", err)
	}
	if err := kvm.Close(); err != nil {
		t.Fatal(err)
	}
	virt, err := exec.CommandContext(ctx, "systemd-detect-virt").Output()
	var exit *exec.ExitError
	if strings.TrimSpace(string(virt)) != "none" || (err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1)) {
		t.Fatal("qualification host is virtualized or detection failed", string(virt), err)
	}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(strings.TrimSpace(string(machine)))
	if err != nil || id == uuid.Nil {
		t.Fatal("native machine identity invalid", err)
	}
	host = id.String()
	bootRaw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	id, err = uuid.Parse(strings.TrimSpace(string(bootRaw)))
	if err != nil || id == uuid.Nil {
		t.Fatal("native boot identity invalid", err)
	}
	return host, id.String()
}

func qualificationSourceCommit(t *testing.T, ctx context.Context) string {
	t.Helper()
	root := filepath.Join("..", "..")
	// The guarded native archive owner writes this marker only after verifying
	// the transferred checkout. A git checkout measures HEAD directly instead.
	marker := filepath.Join(root, ".faas-runtime-qualification-source-sha")
	raw, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		raw, err = exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
	}
	if err != nil {
		t.Fatal("verified native source identity unavailable", err)
	}
	return strings.TrimSpace(string(raw))
}

func qualificationAssets(t *testing.T, assets map[string]string) {
	t.Helper()
	for path, want := range assets {
		//nolint:forbidigo,gosec // Pinned, operator-selected native assets; stream hashes before and after use.
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		size, readErr := io.Copy(h, file)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		if size == 0 || hex.EncodeToString(h.Sum(nil)) != want {
			t.Fatal("native asset differs from pinned digest", path)
		}
	}
}

func qualificationGuestInit(t *testing.T, ctx context.Context, base string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "debugfs", "-R", "cat /sbin/init", base)
	h := sha256.New()
	cmd.Stdout = h
	if err := cmd.Run(); err != nil {
		t.Fatal("measure guest-init from exact base", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func qualificationHTTP(t *testing.T, ctx context.Context, address, want string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("native fixture readiness/content request", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, api.RuntimeReleaseSidecarMaxBytes))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != want {
		t.Fatal("native fixture response differs from selected runtime", response.StatusCode, string(body))
	}
}
