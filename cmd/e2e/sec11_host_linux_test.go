// sec11_host_linux_test.go — M8 §11 host-layer checks that only make
// sense on the production EX44 kernel.
//
// Spec §11 lists a handful of host baseline requirements (cgroups v2,
// kernel ≥ 6.8 HWE, unprivileged_userns_clone=0, unattended-upgrades
// security-only, nftables default-drop inbound). These are pinned
// here at the e2e level so a regression on the operator's bootstrap
// is caught even when the e2e suite runs on bare metal. On macOS
// dev (the default local loop), every test here skips via the
// //go:build linux directive below — the file is not compiled into
// the test binary at all on darwin. On the GitHub ubuntu-latest
// path the same skip fires via `runtime.GOOS != "linux"` (the test
// runs but doesn't actually exercise the host).
//
// The `make egress-check` Makefile target is the existing gate for
// the nftables artifact; we replicate its assertion here
// (TestSec11_NftablesPolicyIsArtifactInSync) so a CI run can pin it
// without spawning a subprocess.

//go:build linux
// +build linux

package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

// --- TestSec11_CgroupsV2Unified ------------------------------------------
//
// §11 "cgroups v2 unified" — the only mode Firecracker snapshot
// restore ships well-under (the Firecracker-on-cgroup-v1 perf trap
// is documented in CLAUDE.md "Gotchas"). This test pins the host
// actually is on v2 at the place that matters: the kernel command
// line. Without `systemd.unified_cgroup_hierarchy=1`, /sys/fs/cgroup
// still exists but the controller tree is v1 (or hybrid), and the
// memory.max write that vmmd relies on would silently no-op.

func TestSec11_CgroupsV2Unified(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux host check")
	}

	// The cgroup.controllers file is only emitted under a unified
	// hierarchy; its presence is the canonical signal.
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Errorf("/sys/fs/cgroup/cgroup.controllers missing — cgroups v2 not enabled: %v", err)
	}

	// Belt-and-suspenders: confirm the kernel actually booted with
	// the unified mode on the command line. Some hybrid modes
	// mount v2 on /sys/fs/cgroup but route controllers through v1.
	if !commandLineHas(t, "systemd.unified_cgroup_hierarchy=1") &&
		!commandLineHas(t, "cgroup_no_v1=") {
		t.Errorf("/proc/cmdline lacks systemd.unified_cgroup_hierarchy=1 and cgroup_no_v1= — " +
			"hybrid hierarchy; spec §11 requires v2")
	}
}

// --- TestSec11_KernelAtLeast68 -------------------------------------------
//
// §11 "kernel ≥ 6.8 HWE" — the EX44 ships Ubuntu 24.04 HWE which is
// 6.8+. Older kernels lack io_uring fixes Firecracker relies on for
// snapshot restore. Bounds major.minor at 6.8 by parsing /proc/version.

func TestSec11_KernelAtLeast68(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux host check")
	}
	maj, min, err := kernelVersion()
	if err != nil {
		t.Fatalf("read /proc/version: %v", err)
	}
	if maj < 6 || (maj == 6 && min < 8) {
		t.Errorf("kernel %d.%d — spec §11 requires ≥ 6.8", maj, min)
	}
}

// --- TestSec11_UnprivilegedUserNSDisabled --------------------------------
//
// §11 "host.unprivileged_userns_clone=0" — the sysctl that denies
// unprivileged user namespaces. The kernel-side primitive is named
// `kernel.unprivileged_userns_clone`; on Ubuntu HWE the ansible
// role writes the value to /etc/sysctl.d/. This pin catches a
// future operator run that forgets to set it.

func TestSec11_UnprivilegedUserNSDisabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux host check")
	}
	v, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone")
	if err != nil {
		t.Fatalf("read sysctl: %v", err)
	}
	if strings.TrimSpace(string(v)) != "0" {
		t.Errorf("kernel.unprivileged_userns_clone = %q — spec §11 requires 0", strings.TrimSpace(string(v)))
	}
}

// --- TestSec11_UnattendedUpgradesSecurityOnly ----------------------------
//
// §11 "unattended-upgrades security-only". The shipped ansible role
// templates /etc/apt/apt.conf.d/20auto-upgrades (daily check +
// download) and /etc/apt/apt.conf.d/50unattended-upgrades (security
// origin filter). A future operator run could either disable
// auto-updates entirely (CVE window blowout) or accidentally allow
// non-security releases (stability regression on the box).

func TestSec11_UnattendedUpgradesSecurityOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux host check")
	}

	auto, err := os.ReadFile("/etc/apt/apt.conf.d/20auto-upgrades")
	if err != nil {
		t.Fatalf("read 20auto-upgrades: %v", err)
	}
	if !strings.Contains(string(auto), `Update-Package-Lists "1"`) ||
		!strings.Contains(string(auto), `Unattended-Upgrade "1"`) {
		t.Errorf("20auto-upgrades missing one of {Update-Package-Lists 1, Unattended-Upgrade 1}:\n%s", auto)
	}

	cfg, err := os.ReadFile("/etc/apt/apt.conf.d/50unattended-upgrades")
	if err != nil {
		t.Fatalf("read 50unattended-upgrades: %v", err)
	}
	// Allowed-Origins must include only the security pocket. Other
	// pockets (updates, backports, proposed) are forbidden on a
	// one-box FaaS host where stability is more valuable than new
	// features.
	if !strings.Contains(string(cfg), "security") {
		t.Errorf("50unattended-upgrades missing 'security' origin:\n%s", cfg)
	}
	for _, bad := range []string{"updates", "backports", "proposed"} {
		if strings.Contains(string(cfg), bad+";") || strings.Contains(string(cfg), `"-`+bad+`"`) {
			t.Errorf("50unattended-upgrades references forbidden pocket %q:\n%s", bad, cfg)
		}
	}
}

// --- TestSec11_NftablesPolicyIsArtifactInSync ----------------------------
//
// §11 "nftables default-drop inbound" — pinned at the artifact layer.
// The shipped policy lives in two places: (a) the rendered commit
// (deploy/ansible/roles/nftables/files/policy_nftables.conf) and
// (b) the Go source of truth (pkg/netns.DefaultHostPolicy.Render()).
// If they ever drift, ansible will overwrite one or the other; the
// `make egress-check` Makefile target already catches the
// drift programmatically; here we replicate the assertion
// in-test so a CI job can run it under `go test`.
//
// No subprocess spawns: pkg/netns.DefaultHostPolicy.Render() runs
// in-process, fast and deterministic. The committed artifact is
// read from repoRoot-relative path.

func TestSec11_NftablesPolicyIsArtifactInSync(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux host check")
	}
	root := repoRootHost()
	if root == "" {
		t.Skip("module root not reachable")
	}
	committedPath := filepath.Join(root,
		"deploy/ansible/roles/nftables/files/policy_nftables.conf")
	committedBytes, err := os.ReadFile(committedPath)
	if err != nil {
		t.Skipf("artifact %s not present in worktree: %v", committedPath, err)
	}
	rendered := netns.DefaultHostPolicy.Render()

	if string(committedBytes) != rendered {
		// The surface is long; show just the first diff position so a
		// reviewer's eye lands on the change.
		committedLines := strings.Split(string(committedBytes), "\n")
		renderedLines := strings.Split(rendered, "\n")
		for i := 0; i < len(committedLines) && i < len(renderedLines); i++ {
			if committedLines[i] != renderedLines[i] {
				t.Errorf("nftables artifact drift at line %d:\n  committed: %s\n  rendered : %s",
					i+1, committedLines[i], renderedLines[i])
				break
			}
		}
		if len(committedLines) != len(renderedLines) {
			t.Errorf("nftables artifact line count differs: committed=%d rendered=%d",
				len(committedLines), len(renderedLines))
		}
	}
}

// --- helpers ------------------------------------------------------------

// repoRootHost resolves the module root by walking up from cwd. Mirrors
// the walk in pkg/e2etest/harness.go but lives here to avoid an
// import cycle (e2etest's harness is heavyweight).
func repoRootHost() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// commandLineHas reports whether /proc/cmdline contains needle.
func commandLineHas(t *testing.T, needle string) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// kernelVersion parses /proc/version, returns (major, minor). Used
// by TestSec11_KernelAtLeast68 only. Returns an error on parse
// failure so the test fatally and the operator sees the actual /proc/version.
func kernelVersion() (int, int, error) {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return 0, 0, err
	}
	// Linux version line: "Linux version 6.8.0-31-generic ..."
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, &parseError{msg: "unexpected /proc/version shape"}
	}
	ver := strings.TrimPrefix(fields[2], "version")
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) < 2 {
		return 0, 0, &parseError{msg: "no minor: " + ver}
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, &parseError{msg: "major: " + err.Error()}
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, &parseError{msg: "minor: " + err.Error()}
	}
	return maj, min, nil
}

type parseError struct{ msg string }

func (e *parseError) Error() string { return "kernelVersion: " + e.msg }
