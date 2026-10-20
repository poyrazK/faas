// adr: 025
// paths_pure_extra_test.go — fill the remaining gaps in
// pkg/sched/paths.go beyond what paths_test.go covers. SnapDir
// override (via SetSnapDirForTesting), AppLayerKey,
// Snapshot*Key wrappers, BaseKey('') empty-runtime branch,
// LayerKey / KernelKey / ParentBaseRuntime, layerKey internal
// helper, IsParentBaseKey missing-key branch.
//
// Whitebox `package sched`.

package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	goruntime "runtime"
	"strings"
	"testing"
)

// --- SnapDir -------------------------------------------------------

func TestPathsExtra_SnapDirOverride(t *testing.T) {
	prev := sched.SnapDir()
	t.Cleanup(func() { sched.SetSnapDirForTesting(prev) })

	sched.SetSnapDirForTesting("/tmp/override-snap")
	if got := sched.SnapDir(); got != "/tmp/override-snap" {
		t.Errorf("after override: got %q", got)
	}

	sched.SetSnapDirForTesting(prev) // restore
	if got := sched.SnapDir(); got != prev {
		t.Errorf("after restore: got %q, want %q", got, prev)
	}
}

// --- AppLayerKey --------------------------------------------------

func TestPathsExtra_AppLayerKey(t *testing.T) {
	if got := sched.AppLayerKey("my-app", "dep-1"); got != "apps/my-app/dep-1.ext4" {
		t.Errorf("got %q", got)
	}
}

// --- Snapshot* wrappers -----------------------------------------

func TestPathsExtra_SnapshotMemKey(t *testing.T) {
	// Pin the canonical shape (delegates to state.SnapMemKey).
	if got := sched.SnapshotMemKey("dep-1"); got == "" {
		t.Error("SnapshotMemKey returned empty")
	} else if !strings.Contains(got, "dep-1") {
		t.Errorf("expected dep-1 substring in %q", got)
	}
}

func TestPathsExtra_SnapshotVMStateKey(t *testing.T) {
	got := sched.SnapshotVMStateKey("dep-1")
	if got == "" {
		t.Error("SnapshotVMStateKey returned empty")
	} else if !strings.Contains(got, "dep-1") {
		t.Errorf("expected dep-1 substring in %q", got)
	}
}

func TestPathsExtra_SnapshotWarmMemKey(t *testing.T) {
	if got := sched.SnapshotWarmMemKey("dep-1"); got == "" {
		t.Error("SnapshotWarmMemKey returned empty")
	}
}

func TestPathsExtra_SnapshotWarmVMStateKey(t *testing.T) {
	if got := sched.SnapshotWarmVMStateKey("dep-1"); got == "" {
		t.Error("SnapshotWarmVMStateKey returned empty")
	}
}

// --- BaseKey empty-runtime branch -------------------------------

func TestPathsExtra_BaseKeyEmptyRuntime(t *testing.T) {
	// paths_test.go covers BaseKey with a runtime; this pins the
	// runtime="" branch (plain apps boot the generic base).
	want := sched.BaseKeyForArch("", goruntime.GOARCH)
	if got := sched.BaseKey(""); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- LayerKey / KernelKey ----------------------------------------

func TestPathsExtra_LayerKey(t *testing.T) {
	if got := sched.LayerKey("dep-1"); got != "layers/dep-1.ext4" {
		t.Errorf("got %q", got)
	}
}

func TestPathsExtra_JobLayerKey(t *testing.T) {
	if got := sched.JobLayerKey("job-1"); got != "jobs/job-1.ext4" {
		t.Errorf("got %q", got)
	}
}

func TestPathsExtra_KernelKey(t *testing.T) {
	if got := sched.KernelKey("v1.7.0"); got != "kernel/v1.7.0" {
		t.Errorf("got %q", got)
	}
}

// --- IsParentBaseKey (extra branches) ----------------------------

func TestPathsExtra_IsParentBaseKey_EmptyFalse(t *testing.T) {
	if sched.IsParentBaseKey("") {
		t.Error("empty: got true, want false")
	}
}

func TestPathsExtra_IsParentBaseKey_UnrelatedFalse(t *testing.T) {
	for _, key := range []string{"apps/my-app/dep-1.ext4", "base/runner-foo.ext4", "layers/x.ext4", "kernel/v1"} {
		if sched.IsParentBaseKey(key) {
			t.Errorf("unrelated key %q: got true, want false", key)
		}
	}
}

func TestPathsExtra_IsParentBaseKey_AliasesTrue(t *testing.T) {
	for _, alias := range sched.ParentBaseKeyAliases() {
		if !sched.IsParentBaseKey(alias) {
			t.Errorf("alias %q: got false, want true", alias)
		}
	}
}

// --- layerKey internal helper ------------------------------------

func TestPathsExtra_LayerKey_RootfsKeyWins(t *testing.T) {
	if got := sched.XLayerKey("rootfs/dep-1.ext4", "dep-1"); got != "rootfs/dep-1.ext4" {
		t.Errorf("got %q", got)
	}
}

func TestPathsExtra_LayerKey_EmptyRootfsFallsBack(t *testing.T) {
	if got := sched.XLayerKey("", "dep-1"); got != "layers/dep-1.ext4" {
		t.Errorf("got %q, want layers/dep-1.ext4", got)
	}
}

// --- baseKey internal alias --------------------------------------

func TestPathsExtra_BaseKey_InternalAlias(t *testing.T) {
	if got := sched.XBaseKey("node22"); got != sched.BaseKey("node22") {
		t.Errorf("got %q", got)
	}
	if got := sched.XBaseKey(""); got != sched.BaseKey("") {
		t.Errorf("empty: got %q", got)
	}
}

// --- ParentBaseRuntime constant ---------------------------------

func TestPathsExtra_ParentBaseRuntime_StableString(t *testing.T) {
	if sched.ParentBaseRuntime != "base-debian-parent" {
		t.Errorf("got %q, want base-debian-parent", sched.ParentBaseRuntime)
	}
}
