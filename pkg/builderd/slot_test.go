package builderd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeResid struct{ mb int }

func (f fakeResid) ResidentMB() int { return f.mb }

func TestSlot_NilProbeGuaranteedOnly(t *testing.T) {
	got := DecideSlot(nil, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("nil probe: got %+v, want guaranteed allowed", got)
	}
}

func TestSlot_BelowThresholdGetsOpportunistic(t *testing.T) {
	// 50% residency → opportunistic slot granted.
	got := DecideSlot(fakeResid{mb: 23_000}, 47_600)
	if !got.Allowed || got.Label != "opportunistic" {
		t.Errorf("50%% residency: got %+v, want opportunistic", got)
	}
}

func TestSlot_AtThresholdGuaranteedOnly(t *testing.T) {
	// 60% residency → threshold (>=); opportunistic denied, guaranteed allowed.
	got := DecideSlot(fakeResid{mb: 28_560}, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("60%% residency: got %+v, want guaranteed", got)
	}
}

// TestSlot_JustBelowThresholdGetsOpportunistic pins the boundary as
// strict greater-than: 60% × 47,600 = 28,560 MB. The test above asserts
// 28,560 falls in the guaranteed-only bucket; this test asserts 28,559
// (one byte under) falls in the opportunistic bucket. If the comparison
// in slot.go ever flips to `>` or `<=` both tests still pass but their
// swap makes the boundary regression visible. (Same boundary coverage
// as TestProcessOne_AppLayerAtCapSucceeds for the AppLayerMaxMB check.)
func TestSlot_JustBelowThresholdGetsOpportunistic(t *testing.T) {
	got := DecideSlot(fakeResid{mb: 28_559}, 47_600)
	if !got.Allowed || got.Label != "opportunistic" {
		t.Errorf("60%%-minus-1MB residency: got %+v, want opportunistic", got)
	}
}

func TestSlot_AboveThresholdGuaranteedOnly(t *testing.T) {
	// 80% residency → guaranteed only.
	got := DecideSlot(fakeResid{mb: 38_000}, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("80%% residency: got %+v, want guaranteed", got)
	}
}

func TestSlot_ZeroCeilingFallsBack(t *testing.T) {
	got := DecideSlot(fakeResid{mb: 1000}, 0)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("zero ceiling: got %+v, want guaranteed (fallback)", got)
	}
}

func TestBuilderSlots_SecondSlotNeedsSliceMemory(t *testing.T) {
	cases := []struct {
		name   string
		freeMB int
		ok     bool
		want   bool
	}{
		{name: "default 5 GiB slice with one build running", freeMB: 5120 - 2*2048 - 64, ok: true, want: false},
		{name: "slice unreadable", ok: false, want: false},
		{name: "one MiB short", freeMB: secondSlotNeedMB - 1, ok: true, want: false},
		{name: "raised slice", freeMB: secondSlotNeedMB, ok: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &Builderd{resid: fakeResid{mb: 0}, sliceFreeMB: func() (int, bool) { return tc.freeMB, tc.ok }}
			first, releaseFirst, ok := b.acquireSlot()
			if !ok || first.Label != "guaranteed" {
				t.Fatalf("first slot = %+v, acquired=%v; want guaranteed", first, ok)
			}
			defer releaseFirst()
			second, releaseSecond, ok := b.acquireSlot()
			if ok != tc.want {
				t.Fatalf("second slot = %+v, acquired=%v; want %v", second, ok, tc.want)
			}
			if ok {
				if second.Label != "opportunistic" {
					t.Fatalf("second slot label = %q, want opportunistic", second.Label)
				}
				defer releaseSecond()
				if third, _, ok := b.acquireSlot(); ok {
					t.Fatalf("third slot = %+v; want the maxBuilderSlots limit", third)
				}
			}
		})
	}
}

func TestBuilderSlots_SecondSlotNeedsLowResidency(t *testing.T) {
	b := &Builderd{resid: fakeResid{mb: api.RAMAdmissionCeilingMB}, sliceFreeMB: func() (int, bool) { return 1 << 20, true }}
	_, release, ok := b.acquireSlot()
	if !ok {
		t.Fatal("guaranteed slot unavailable")
	}
	defer release()
	if got, _, ok := b.acquireSlot(); ok {
		t.Fatalf("second slot at full tenant residency = %+v; want denial", got)
	}
}

func TestCgroupFreeMB(t *testing.T) {
	write := func(t *testing.T, max, current string) string {
		dir := t.TempDir()
		if max != "" {
			if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte(max), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if current != "" {
			if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte(current), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	cases := []struct {
		name, max, current string
		want               int
		ok                 bool
	}{
		{name: "free", max: "10737418240\n", current: "1073741824\n", want: 9216, ok: true},
		{name: "over", max: "1048576", current: "2097152", want: 0, ok: true},
		{name: "unlimited", max: "max\n", current: "1", ok: false},
		{name: "missing", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cgroupFreeMB(write(t, tc.max, tc.current))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("cgroupFreeMB = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestBuilderSlots_NoResidencyProbeKeepsSecondClosed(t *testing.T) {
	b := &Builderd{}
	_, release, ok := b.acquireSlot()
	if !ok {
		t.Fatal("guaranteed slot should be available without a probe")
	}
	defer release()

	got, _, ok := b.acquireSlot()
	if ok || got.Allowed {
		t.Fatalf("second slot without probe = %+v, acquired=%v; want denial", got, ok)
	}
}
