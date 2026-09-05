//go:build metal

package fcvm

import (
	"debug/elf"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A test executable neither implements vmmd's helper modes nor necessarily
// links statically. Use the same checkout's real vmmd, built by run-metal.sh.
func newMetalVMM(t *testing.T, timeout time.Duration) *JailerVMM {
	t.Helper()
	path := os.Getenv("FAAS_TEST_VMMD_BINARY")
	if path == "" {
		t.Fatal("set FAAS_TEST_VMMD_BINARY to a static vmmd built from this checkout (deploy/lima/run-metal.sh does this)")
	}
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("open mount helper: %v", err)
	}
	defer f.Close()
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			t.Fatal("mount helper requires a dynamic loader; rebuild vmmd with CGO_ENABLED=0")
		}
	}
	v := NewJailerVMM(JailChrootBase, timeout)
	v.mountHelperPath = path
	// Retain rings before failure cleanup unregisters them; report the bounded
	// console tail when a metal test fails instead of losing the boot diagnosis.
	stop, stopped := make(chan struct{}), make(chan struct{})
	seen := make(map[string]*logbuf.Ring)
	go func() {
		defer close(stopped)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				v.mu.Lock()
				for id, ring := range v.rings {
					seen[id] = ring
				}
				v.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-stopped
		if t.Failed() {
			for id, ring := range seen {
				lines := ring.Snapshot(1)
				if len(lines) > 100 {
					lines = lines[len(lines)-100:]
				}
				for _, line := range lines {
					t.Logf("guest %s: %s", id, line.Line)
				}
			}
		}
	})
	return v
}

// NewAcceptanceManager exposes the real manager to external-package tests that
// also import builderd/imaged. Keeping this seam in _test.go avoids a production
// environment override for the privileged mount helper.
func NewAcceptanceManager(t *testing.T) *Manager {
	t.Helper()
	withCgroupRootAt(t, "/sys/fs/cgroup")
	backend, err := storage.NewLocalStorageBackend("/srv/fc")
	if err != nil {
		t.Fatal(err)
	}
	v := newMetalVMM(t, 30*time.Second).WithStorage(backend)
	return NewManager(wire.ExecRunner{}, v, Paths{Kernel: os.Getenv("FAAS_TEST_KERNEL")}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
}
