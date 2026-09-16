package e2etest

// guestconsole.go — surface the guest serial console in failure output.
//
// vmmd writes every microVM's serial console to
// /var/log/faas/vm-<instance>.console (pkg/fcvm/vmm.go). It is written on the
// host, outside the jail chroot, and survives the VM's teardown — which makes
// it the ONLY view inside a guest after the fact. When a wake fails with
//
//	readiness: app_startup_timeout: guest <id> not ready after 30s:
//	startup_phase=guest_startup; no readiness connection was accepted
//
// every daemon log on the host says the same thing — "the guest never called
// back" — and none of them can say why. The console can: a kernel that never
// mounted its rootfs, a guest-init that panicked, an app that exited. The
// harness dumped daemon output on failure but never these, so every guest-side
// failure in the gate read as a host-side timeout (smoke run 35160982633,
// three image-deploy tests).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GuestConsoleDir is where vmmd writes serial consoles.
const GuestConsoleDir = "/var/log/faas"

// consoleTailBytes bounds each console in the failure report.
const consoleTailBytes = 8 * 1024

// consoleTail is one guest's console, trimmed for a failure report.
type consoleTail struct {
	Path     string
	Modified time.Time
	Bytes    int64  // whole file
	Tail     string // last consoleTailBytes
}

// recentConsoleTails returns the consoles under dir modified at or after
// since — the guests this harness booted — newest last. A missing directory
// is not an error: a host without vmmd simply has no consoles.
func recentConsoleTails(dir string, since time.Time) ([]consoleTail, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "vm-*.console"))
	if err != nil {
		return nil, err
	}
	var out []consoleTail
	for _, p := range matches {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			out = append(out, consoleTail{Path: p, Modified: info.ModTime(), Bytes: info.Size(), Tail: fmt.Sprintf("(unreadable: %v)", err)})
			continue
		}
		if len(data) > consoleTailBytes {
			data = data[len(data)-consoleTailBytes:]
		}
		out = append(out, consoleTail{Path: p, Modified: info.ModTime(), Bytes: info.Size(), Tail: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.Before(out[j].Modified) })
	return out, nil
}

// renderConsoleTails formats consoles for t.Logf; empty when there are none.
func renderConsoleTails(tails []consoleTail) string {
	if len(tails) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range tails {
		fmt.Fprintf(&b, "==> %s (%d bytes, modified %s)\n%s\n", c.Path, c.Bytes, c.Modified.UTC().Format(time.RFC3339), strings.TrimRight(c.Tail, "\n"))
	}
	return b.String()
}
