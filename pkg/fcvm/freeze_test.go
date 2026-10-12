// adr: 906 — a frozen instance is paused and reclaimed in place, and every
// failed freeze leaves the guest running.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type freezingVMM struct {
	*fakeVMM
	calls      []string
	pauseErr   error
	reclaimErr error
}

func (v *freezingVMM) PauseVM(_ context.Context, l Lease) error {
	v.calls = append(v.calls, "pause:"+l.Instance)
	return v.pauseErr
}

func (v *freezingVMM) ReclaimVMMemory(l Lease) (VMMemoryReclaim, error) {
	v.calls = append(v.calls, "reclaim:"+l.Instance)
	return VMMemoryReclaim{BeforeBytes: 200 << 20, AfterBytes: 60 << 20, SwappedBytes: 120 << 20}, v.reclaimErr
}

func TestFreezeThaw(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name       string
		pauseErr   error
		reclaimErr error
		wantCalls  []string
		wantResume bool
	}{
		{name: "frozen", wantCalls: []string{"pause:f", "reclaim:f"}},
		{name: "pause fails", pauseErr: boom, wantCalls: []string{"pause:f"}, wantResume: true},
		{name: "reclaim fails", reclaimErr: boom, wantCalls: []string{"pause:f", "reclaim:f"}, wantResume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &freezingVMM{fakeVMM: &fakeVMM{}, pauseErr: tc.pauseErr, reclaimErr: tc.reclaimErr}
			m := newTestManager(&fakeRunner{}, v)
			if _, err := m.Wake(t.Context(), admissionWake("f")); err != nil {
				t.Fatal(err)
			}
			rep, err := m.Freeze(t.Context(), "f")
			if failed := tc.pauseErr != nil || tc.reclaimErr != nil; failed != (err != nil) || (failed && !errors.Is(err, boom)) {
				t.Fatalf("Freeze error = %v", err)
			}
			if !slices.Equal(v.calls, tc.wantCalls) {
				t.Fatalf("VMM calls = %v, want %v", v.calls, tc.wantCalls)
			}
			if resumed := slices.Contains(v.resumed, "f"); resumed != tc.wantResume {
				t.Fatalf("resumed after freeze = %v, want %v", resumed, tc.wantResume)
			}
			if err == nil && (rep.BeforeBytes != 200<<20 || rep.AfterBytes != 60<<20 || rep.SwappedBytes != 120<<20) {
				t.Fatalf("report = %+v", rep)
			}
			if m.LiveCount() != 1 || len(m.instanceFlights) != 0 {
				t.Fatalf("live=%d flights=%d after freeze, want 1/0", m.LiveCount(), len(m.instanceFlights))
			}
			if err := m.Thaw(t.Context(), "f"); err != nil {
				t.Fatalf("Thaw: %v", err)
			}
			if v.resumed[len(v.resumed)-1] != "f" {
				t.Fatalf("Thaw did not resume the VM: %v", v.resumed)
			}
			if err := m.Destroy(t.Context(), "f"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFreezeRefusals(t *testing.T) {
	plain := newTestManager(&fakeRunner{}, &fakeVMM{})
	if _, err := plain.Wake(t.Context(), admissionWake("p")); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Freeze(t.Context(), "p"); err == nil {
		t.Fatal("Freeze succeeded on a VMM without the capability")
	}

	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeRunner{}, v)
	if _, err := m.Freeze(t.Context(), "missing"); err == nil {
		t.Fatal("Freeze succeeded on an instance that is not live")
	}
	task := admissionWake("task")
	task.AppTaskOnly = true
	if _, err := m.Wake(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Freeze(t.Context(), "task"); err == nil {
		t.Fatal("Freeze succeeded on an app task instance")
	}
	if len(v.calls) != 0 {
		t.Fatalf("refused freezes reached the VMM: %v", v.calls)
	}
}

// The fake memory.reclaim is a regular file, so it holds the last chunk
// requested: file pages (20 MiB) arrive as 16 + 4 MiB, and file plus anon
// pages (28 MiB) as 16 + 12 MiB.
func TestReclaimVMMemoryRequestsReclaimablePagesInChunks(t *testing.T) {
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	saved := procMeminfoPath
	procMeminfoPath = meminfo
	t.Cleanup(func() { procMeminfoPath = saved })
	for _, tc := range []struct {
		lease     Lease
		swapMax   string
		swapFree  string
		lastChunk int64
	}{
		{lease: Lease{Instance: "noswap", Plan: api.PlanHobby}, swapMax: "max", swapFree: "0", lastChunk: 4 << 20},
		{lease: Lease{Instance: "swap", Plan: api.PlanHobby}, swapMax: "max", swapFree: "1048576", lastChunk: 12 << 20},
		{lease: Lease{Instance: "swapoff", Plan: api.PlanPro}, swapMax: "0", swapFree: "1048576", lastChunk: 4 << 20},
		{lease: Lease{Instance: "build", IsBuilder: true}, swapMax: "max", swapFree: "1048576", lastChunk: 12 << 20},
	} {
		parent := ParentCgroupFor(tc.lease.Plan)
		if tc.lease.IsBuilder {
			parent = BuilderCgroupParent
		}
		scope := filepath.Join(cgroupRoot, parent, PerInstanceScope(tc.lease.Instance))
		if err := os.MkdirAll(scope, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"memory.current":      "41943040\n",
			"memory.stat":         "anon 8388608\nfile 20971520\nkernel 1048576\n",
			"memory.swap.current": "1024\n",
			"memory.swap.max":     tc.swapMax + "\n",
			"memory.reclaim":      "",
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(scope, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(meminfo, []byte("SwapTotal: 1048576 kB\nSwapFree: "+tc.swapFree+" kB\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := (&JailerVMM{}).ReclaimVMMemory(tc.lease)
		if err != nil {
			t.Fatalf("%s: %v", tc.lease.Instance, err)
		}
		if r != (VMMemoryReclaim{BeforeBytes: 40 << 20, AfterBytes: 40 << 20, SwappedBytes: 1024}) {
			t.Fatalf("%s: reclaim = %+v", tc.lease.Instance, r)
		}
		got, err := os.ReadFile(filepath.Join(scope, "memory.reclaim"))
		if err != nil || string(got) != strconv.FormatInt(tc.lastChunk, 10) {
			t.Fatalf("%s: last memory.reclaim request = %q, %v; want %d", tc.lease.Instance, got, err, tc.lastChunk)
		}
	}
}
