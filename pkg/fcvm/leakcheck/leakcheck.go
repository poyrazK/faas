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
//  4. No per-VM cgroup leaves under faas-tenant.slice (scope name is
//     the Lease.Instance verbatim — see pkg/fcvm.PerInstanceScope).
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
	"strconv"
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
		if isTenantNetdev(dev) {
			errs = append(errs, fmt.Errorf("netdev %s", dev))
		}
	}

	for _, dir := range listJailChroots() {
		errs = append(errs, fmt.Errorf("jail chroot %s", dir))
	}

	for _, scope := range listVMScopes("/sys/fs/cgroup") {
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
// after a teardown. The dir is /srv/fc/jail/<firecracker-binary>/<id>/root per
// pkg/fcvm/vmm.go (chrootRoot).
func listJailChroots() []string { return jailChrootsAt("/srv/fc/jail") }

// Jailer uses the resolved Firecracker binary basename, which commonly includes
// its version. Empty version parents are reusable infrastructure, not VM leaks.
func jailChrootsAt(root string) []string {
	dirs, _ := filepath.Glob(filepath.Join(root, "firecracker*", "*"))
	var out []string
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// Cover current plan slices and the dedicated builder slice, plus the legacy
// tenant layout. Do not mistake empty plan parents for per-instance scopes.
func listVMScopes(root string) []string {
	patterns := []string{
		"faas.slice/faas-tenant.slice/tenant-*/*",
		"faas.slice/faas-cp.slice/faas-cp-build.slice/*",
		"faas-tenant.slice/*",
	}
	var out []string
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range paths {
			if filepath.Dir(path) == filepath.Join(root, "faas-tenant.slice") {
				switch filepath.Base(path) {
				case "tenant-free", "tenant-hobby", "tenant-pro", "tenant-scale":
					continue
				}
			}
			if _, err := os.Stat(filepath.Join(path, "cgroup.procs")); err == nil {
				out = append(out, path)
			}
		}
	}
	return out
}

// Current host-side veth names are vh<slot>; older fixtures used ve-*.
func isTenantNetdev(name string) bool {
	if strings.HasPrefix(name, "tap-") || strings.HasPrefix(name, "ve-") {
		return true
	}
	name = strings.SplitN(name, "@", 2)[0]
	if !strings.HasPrefix(name, "vh") {
		return false
	}
	_, err := strconv.ParseUint(strings.TrimPrefix(name, "vh"), 10, 32)
	return err == nil
}
