//go:build linux

// Tests for the per-workload cgroup v2 partition helpers
// (issue #463 / ADR-069 / PR-B AC #4). The pure-function
// tests (cgroupSafeName, leafDir) don't require a cgroup
// mount — they exercise the name derivation that the
// caller relies on to keep workloads from escaping the
// cgroup hierarchy via path manipulation.
//
// The partitionInto test uses a temporary cgroupRoot so it can
// verify the memory.max bytes without a real mount. The
// atomic launch / mountCgroup2 tests still require a real cgroup
// v2 mount and root permissions; those run as part of the metal
// suite (TestMetalSidecarCgroupPartition in
// pkg/fcvm/manager_metal_test.go).

package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareWorkloadCgroupUsesRequestedMemory(t *testing.T) {
	oldRoot := cgroupRoot
	cgroupRoot = t.TempDir()
	t.Cleanup(func() { cgroupRoot = oldRoot })

	leaf, err := prepareWorkloadCgroup("main", "app", 256, slog.Default())
	if err != nil {
		t.Fatalf("prepareWorkloadCgroup: %v", err)
	}
	if leaf == "" {
		t.Fatal("prepareWorkloadCgroup returned empty leaf for a configured workload")
	}
	body, err := os.ReadFile(filepath.Join(leaf, "memory.max"))
	if err != nil {
		t.Fatalf("read main memory.max: %v", err)
	}
	want := strconv.FormatInt(256<<20, 10) + "\n"
	if string(body) != want {
		t.Fatalf("main memory.max = %q, want %q", body, want)
	}
	cpuLeaf, err := prepareWorkloadCgroup("sidecar", "metrics", 0, slog.Default(), 500)
	if err != nil {
		t.Fatalf("prepareWorkloadCgroup cpu-only: %v", err)
	}
	cpuBody, err := os.ReadFile(filepath.Join(cpuLeaf, "cpu.max"))
	if err != nil {
		t.Fatalf("read sidecar cpu.max: %v", err)
	}
	if got, want := string(cpuBody), "50000 100000\n"; got != want {
		t.Fatalf("sidecar cpu.max = %q, want %q", got, want)
	}
	ioLeaf, err := prepareWorkloadCgroupWithIO("sidecar", "io", 0, slog.Default(), 0, "high")
	if err != nil {
		t.Fatalf("prepareWorkloadCgroup disk io: %v", err)
	}
	ioBody, err := os.ReadFile(filepath.Join(ioLeaf, "io.weight"))
	if err != nil {
		t.Fatalf("read sidecar io.weight: %v", err)
	}
	if got, want := string(ioBody), "200\n"; got != want {
		t.Fatalf("sidecar io.weight = %q, want %q", got, want)
	}
	if _, err := prepareWorkloadCgroupWithIO("sidecar", "bad-profile", 0, slog.Default(), 0, "burst"); err == nil {
		t.Fatal("invalid disk io profile should fail closed")
	}
	if got, err := prepareWorkloadCgroup("main", "app", 0, slog.Default()); err != nil || got != "" {
		t.Fatalf("zero RAM should skip the child cgroup, got %q", got)
	}
	if _, err := prepareWorkloadCgroup("sidecar", "bad/name", 64, slog.Default()); err == nil {
		t.Fatal("invalid workload name should fail closed before exec")
	}
}

func TestUpdateMainWorkloadCPULimitWritesLiveLeaf(t *testing.T) {
	oldRoot := cgroupRoot
	cgroupRoot = t.TempDir()
	t.Cleanup(func() { cgroupRoot = oldRoot })
	leaf := leafDir("main", "app")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("create main workload cgroup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "cpu.max"), []byte("25000 100000\n"), 0o644); err != nil {
		t.Fatalf("seed main cpu.max: %v", err)
	}
	if err := updateMainWorkloadCPULimit(500); err != nil {
		t.Fatalf("updateMainWorkloadCPULimit: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(leaf, "cpu.max"))
	if err != nil {
		t.Fatalf("read updated main cpu.max: %v", err)
	}
	if got, want := string(body), "50000 100000\n"; got != want {
		t.Fatalf("main cpu.max = %q, want %q", got, want)
	}
	if err := updateMainWorkloadCPULimit(333); err == nil {
		t.Fatal("invalid live CPU policy should fail closed")
	}
}

// TestCgroupSafeName_ValidCombinations pins the happy path:
// type + name joined with a single dash, used to derive the
// per-workload cgroup leaf path. The cgroup v2 kernel
// rejects slashes in leaf names; this test catches a
// regression that lets a workload's name leak through as a
// path separator.
func TestCgroupSafeName_ValidCombinations(t *testing.T) {
	cases := []struct {
		typ, name, want string
	}{
		{"init", "migrator", "init-migrator"},
		{"sidecar", "scraper", "sidecar-scraper"},
		{"main", "app", "main-app"},
		// Names with internal dashes are valid; the
		// function preserves them (kernel allows).
		{"sidecar", "log-shipper", "sidecar-log-shipper"},
		// Numbers in the name are valid.
		{"init", "init1", "init-init1"},
	}
	for _, tc := range cases {
		t.Run(tc.typ+"_"+tc.name, func(t *testing.T) {
			got := cgroupSafeName(tc.typ, tc.name)
			if got != tc.want {
				t.Errorf("cgroupSafeName(%q, %q) = %q, want %q", tc.typ, tc.name, got, tc.want)
			}
		})
	}
}

// TestCgroupSafeName_RejectsPathEscape pins the
// path-escape guard: a workload whose name contains / or
// \\ or a NUL byte MUST NOT produce a leaf name that could
// escape /sys/fs/cgroup. An empty result signals the
// caller to skip the partition (the workload runs under
// the parent scope, which still has the host-side cap).
func TestCgroupSafeName_RejectsPathEscape(t *testing.T) {
	cases := []struct {
		name, value string
	}{
		{"slash-injection", "evil/name"},
		{"backslash-injection", `evil\name`},
		{"nul-byte-injection", "evil\x00name"},
		{"dotdot-escape", "../etc"},
		{"dotdot-middle", "name/../../etc"},
		{"dotdot-suffix", "name.."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cgroupSafeName("init", tc.value)
			if got != "" {
				t.Errorf("cgroupSafeName must reject %q, got %q", tc.value, got)
			}
		})
	}
}

// TestCgroupSafeName_RejectsEmpty pins the empty-input
// guard. Both empty type and empty name return "" so the
// caller skips the partition — an empty leaf name would
// resolve to the cgroup root, defeating the per-workload
// partition (writing into root memory.max affects every
// cgroup in the tree).
func TestCgroupSafeName_RejectsEmpty(t *testing.T) {
	if got := cgroupSafeName("", "main"); got != "" {
		t.Errorf("cgroupSafeName(empty type) = %q, want \"\"", got)
	}
	if got := cgroupSafeName("main", ""); got != "" {
		t.Errorf("cgroupSafeName(empty name) = %q, want \"\"", got)
	}
	if got := cgroupSafeName("", ""); got != "" {
		t.Errorf("cgroupSafeName(both empty) = %q, want \"\"", got)
	}
}

// TestLeafDir_PinsMountpoint pins the leaf path derivation:
// leafDir joins cgroupRoot + safe name. A regression that
// drifts the mountpoint (e.g. to /sys/fs/cgroup/faas)
// would silently break the partition because the
// partitionInto / atomic-launch writes would land in a
// non-mounted subtree.
func TestLeafDir_PinsMountpoint(t *testing.T) {
	got := leafDir("init", "migrator")
	want := cgroupRoot + "/init-migrator"
	if got != want {
		t.Errorf("leafDir = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, cgroupRoot) {
		t.Errorf("leafDir must root at %q, got %q", cgroupRoot, got)
	}
}

// TestLeafDir_EmptySafeNameReturnsEmpty pins the
// skipped-partition outcome: leafDir returns "" when the
// name fails cgroupSafeName (path escape, empty input).
// The caller checks for "" to skip the partition without
// an error — the workload still runs, just at the parent
// cgroup scope.
func TestLeafDir_EmptySafeNameReturnsEmpty(t *testing.T) {
	if got := leafDir("init", "../etc"); got != "" {
		t.Errorf("leafDir(escape attempt) = %q, want \"\"", got)
	}
	if got := leafDir("", "main"); got != "" {
		t.Errorf("leafDir(empty type) = %q, want \"\"", got)
	}
}

func TestAttachWorkloadCgroupPreservesLaunchAttributes(t *testing.T) {
	credential := &syscall.Credential{Uid: 1001, Gid: 2001}
	original := &syscall.SysProcAttr{Credential: credential, Chroot: "/image", Setpgid: true}
	cmd := exec.Command("/unused")
	cmd.SysProcAttr = original
	file, err := attachWorkloadCgroup(cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if cmd.SysProcAttr == original || original.UseCgroupFD {
		t.Fatal("cgroup attachment mutated shared probe/process attributes")
	}
	attr := cmd.SysProcAttr
	if !attr.UseCgroupFD || attr.CgroupFD != int(file.Fd()) || attr.Credential != credential || attr.Chroot != "/image" || !attr.Setpgid {
		t.Fatalf("launch attributes lost: %+v", attr)
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("cgroup descriptor must be close-on-exec: flags=%d err=%v", flags, err)
	}
}

func TestAttachWorkloadCgroupRejectsSymlinkAndMissingLeaf(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{link, filepath.Join(dir, "missing")} {
		cmd := exec.Command("/unused")
		if file, err := attachWorkloadCgroup(cmd, leaf); err == nil || file != nil || cmd.SysProcAttr != nil {
			t.Fatalf("invalid leaf accepted: leaf=%q file=%v err=%v", leaf, file, err)
		}
	}
	if file, err := attachWorkloadCgroup(nil, dir); err == nil || file != nil {
		t.Fatal("nil command accepted")
	}
}

func TestWorkloadCgroupLaunchFailsClosed(t *testing.T) {
	// A regular directory cannot be a clone3 cgroup target. Neither an
	// unsupported clone3 nor a refused placement may run the command.
	marker := filepath.Join(t.TempDir(), "executed")
	cmd := exec.Command("/bin/sh", "-c", `printf launched > "$1"`, "sh", marker)
	file, err := attachWorkloadCgroup(cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := cmd.Run(); err == nil {
		t.Fatal("command ran outside a real cgroup")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command executed despite failed placement: %v", err)
	}
}

func TestCgroupSafeNameRejectsTypeEscape(t *testing.T) {
	for _, typ := range []string{"../main", "main/child", `main\child`, "main..", "main\x00"} {
		if got := cgroupSafeName(typ, "app"); got != "" {
			t.Fatalf("type escape accepted: %q => %q", typ, got)
		}
	}
}

func TestWorkloadCgroupPartitionRejectsFilesystemEscape(t *testing.T) {
	oldRoot := cgroupRoot
	cgroupRoot = t.TempDir()
	t.Cleanup(func() { cgroupRoot = oldRoot })
	outside := t.TempDir()
	link := filepath.Join(cgroupRoot, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{cgroupRoot, outside, link, filepath.Join(cgroupRoot, "..", "outside")} {
		if err := partitionIntoWithIO(leaf, 64, 250, ""); err == nil {
			t.Fatalf("cgroup partition accepted filesystem escape: %q", leaf)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "memory.max")); !os.IsNotExist(err) {
		t.Fatalf("cgroup control write escaped workload root: %v", err)
	}
}

func TestUpdateMainWorkloadCPULimitRejectsFilesystemEscape(t *testing.T) {
	oldRoot := cgroupRoot
	cgroupRoot = t.TempDir()
	t.Cleanup(func() { cgroupRoot = oldRoot })
	outside := t.TempDir()
	control := filepath.Join(outside, "cpu.max")
	const original = "10000 100000\n"
	if err := os.WriteFile(control, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cgroupRoot, "main-app")); err != nil {
		t.Fatal(err)
	}
	if err := updateMainWorkloadCPULimit(500); err == nil {
		t.Fatal("live update followed escaping workload symlink")
	}
	body, err := os.ReadFile(control)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("outside control changed: %q", body)
	}
}
