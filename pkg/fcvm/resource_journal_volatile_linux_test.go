//go:build linux

// adr: 20261026113920 — the volatile journal reads the real kernel boot id and
// refuses a directory that is not tmpfs.

package fcvm

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestVolatileJournalHostProbes(t *testing.T) {
	boot, err := kernelBootID()
	if err != nil || !looksLikeInstanceID(boot) {
		t.Fatalf("kernelBootID = %q, %v", boot, err)
	}
	mounts, err := os.Open("/proc/self/mounts")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mounts.Close() }()
	checked := false
	for s := bufio.NewScanner(mounts); s.Scan(); {
		f := strings.Fields(s.Text())
		if len(f) < 3 || (f[1] != "/dev/shm" && f[1] != "/run") {
			continue
		}
		got, err := onTmpfs(f[1])
		if err != nil {
			t.Fatal(err)
		}
		if got != (f[2] == "tmpfs") {
			t.Fatalf("onTmpfs(%s) = %v, mounts say %s", f[1], got, f[2])
		}
		checked = true
	}
	if !checked {
		t.Skip("neither /dev/shm nor /run is a mount point here")
	}
}
