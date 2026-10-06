//go:build linux && metal

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestWorkloadCgroupAtomicLaunch requires an explicitly supplied cgroup v2
// parent with memory/cpu delegated to children. It owns only its temporary
// leaf; it never changes the supplied parent's controllers or membership.
func TestWorkloadCgroupAtomicLaunch(t *testing.T) {
	parent := os.Getenv("FAAS_TEST_CGROUP_PARENT")
	if parent == "" {
		t.Skip("FAAS_TEST_CGROUP_PARENT unset; atomic cgroup acceptance not run")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(parent, &stat); err != nil || stat.Type != unix.CGROUP2_SUPER_MAGIC {
		t.Fatalf("acceptance parent is not cgroup v2: type=%x err=%v", stat.Type, err)
	}
	oldRoot := cgroupRoot
	cgroupRoot = parent
	t.Cleanup(func() { cgroupRoot = oldRoot })
	leaf, err := os.MkdirTemp(parent, "gregale-launch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(leaf); err != nil {
			t.Errorf("remove acceptance cgroup: %v", err)
		}
	})
	// Existing interface files are mandatory. A missing delegated controller
	// must fail qualification rather than leave a test running unbounded.
	for _, control := range []string{"memory.max", "cpu.max"} {
		if _, err := os.Stat(filepath.Join(leaf, control)); err != nil {
			t.Fatalf("controller not delegated: %s: %v", control, err)
		}
	}
	if err := partitionIntoWithIO(leaf, 128, 250, ""); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestWorkloadCgroupChild$")
	cmd.Env = append(os.Environ(), "GREGALE_CGROUP_CHILD=parent")
	file, err := attachWorkloadCgroup(cmd, leaf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("atomic workload launch: %v: %s", err, out)
	}
	var got struct{ Parent, Child string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode membership: %v: %s", err, out)
	}
	wantSuffix := "/" + filepath.Base(leaf)
	if !strings.HasSuffix(strings.TrimSpace(got.Parent), wantSuffix) || got.Parent != got.Child {
		t.Fatalf("startup or descendant escaped workload cgroup: %+v", got)
	}
	// Exec probes copy these attributes and must join the same leaf.
	env := append(os.Environ(), "GREGALE_CGROUP_CHILD=leaf")
	report := execHealthcheckWithOptions(ctx, []string{binary, "-test.run=^TestWorkloadCgroupChild$"}, 5*time.Second, 0, env, "", cmd.SysProcAttr, nil)
	if report.Status != healthcheckStatusPass || string(report.Output) != got.Parent {
		t.Fatalf("exec probe escaped workload cgroup: %+v", report)
	}
}

// Invoked only by the test process above. Exit bypasses the testing package's
// PASS line, leaving stdout as machine-readable process membership evidence.
func TestWorkloadCgroupChild(t *testing.T) {
	mode := os.Getenv("GREGALE_CGROUP_CHILD")
	if mode == "" {
		return
	}
	body, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if mode == "leaf" {
		_, _ = os.Stdout.Write(body)
		os.Exit(0)
	}
	binary, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(binary, "-test.run=^TestWorkloadCgroupChild$")
	child.Env = append(os.Environ(), "GREGALE_CGROUP_CHILD=leaf")
	out, err := child.CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, err, string(out))
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct{ Parent, Child string }{string(body), string(out)}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
