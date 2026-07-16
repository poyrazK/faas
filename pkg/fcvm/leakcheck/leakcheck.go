//go:build metal

// Package leakcheck mirrors `deploy/scripts/leakcheck.sh` in pure Go so
// metal-tagged tests can assert the §6.2-4/5 invariant in-process —
// without spawning bash, and without needing the script to be installed
// system-wide.
//
// Checks (same as the shell helper):
//
//  1. No leftover fc-* network namespaces (the per-instance netns name
//     format from ADR-009).
//  2. No tap-* or ve-* devices in the root netns (orphaned veth/tap).
//  3. No jail chroots under /srv/fc/jail/firecracker/<id>/root.
//  4. No vm-*.scope cgroup leaves under faas-tenant.slice.
//
// On non-Linux (macOS dev box) every check is a no-op: invariant §6.2
// only applies to a one-box production host.
package leakcheck

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// AssertZero fails t with a LEAK: line for every leaked resource found.
// On non-Linux hosts it skips entirely (mirrors the shell version's
// "not Linux — skipping" early-out).
func AssertZero(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("leakcheck: not Linux — skipping (run on the EX44 / metal CI)")
		return
	}

	if errs := Zero(); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("leakcheck found leaks:\n  - %s", strings.Join(msgs, "\n  - "))
	}
}

// Zero returns one error per leaked resource. Callers that want a
// different reporting style (e.g. structured logging) use this directly.
func Zero() []error {
	var errs []error

	for _, ns := range listNetns() {
		if strings.HasPrefix(ns, "fc-") {
			errs = append(errs, fmt.Errorf("netns %s", ns))
		}
	}

	for _, dev := range listRootNetdevs() {
		if strings.HasPrefix(dev, "tap-") || strings.HasPrefix(dev, "ve-") {
			errs = append(errs, fmt.Errorf("netdev %s", dev))
		}
	}

	for _, dir := range listJailChroots() {
		errs = append(errs, fmt.Errorf("jail chroot %s", dir))
	}

	for _, scope := range listTenantScopes() {
		errs = append(errs, fmt.Errorf("cgroup %s", scope))
	}

	return errs
}

// listNetns runs `ip netns list` and returns the names of every named
// netns on the host.
func listNetns() []string {
	out, err := exec.Command("ip", "netns", "list").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names
}

// listRootNetdevs runs `ip -o link show` and returns interface names in
// the root netns. The guest-facing tap lives in a tenant netns
// (ADR-009), so a tap-/ve- leaking back into root is always wrong.
func listRootNetdevs() []string {
	out, err := exec.Command("ip", "-o", "link", "show").Output()
	if err != nil {
		return nil
	}
	var names []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		// lines look like: "3: tap0: <BROADCAST,UP,LOWER_UP> ..."
		fields := strings.SplitN(sc.Text(), ":", 3)
		if len(fields) >= 2 {
			names = append(names, strings.TrimSpace(fields[1]))
		}
	}
	return names
}

// listJailChroots returns the jail chroot dirs jailer leaves behind
// after a teardown. The dir is /srv/fc/jail/firecracker/<id>/root per
// pkg/fcvm/vmm.go (chrootRoot).
func listJailChroots() []string {
	root := "/srv/fc/jail/firecracker"
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil // dir missing == nothing to clean up
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name(), "root"))
		}
	}
	return dirs
}

// listTenantScopes globs vm-*.scope under faas-tenant.slice. The path
// is mounted via cgroupv2 (the cgroups_v2 role in deploy/ansible
// asserts this).
func listTenantScopes() []string {
	base := "/sys/fs/cgroup/faas-tenant.slice"
	entries, err := filepath.Glob(filepath.Join(base, "vm-*.scope"))
	if err != nil {
		return nil
	}
	return entries
}
