package fcvm

// adr: 20261109172421 — step 6.5, flat tenant cgroups.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func withCgroupRoot(t *testing.T) string {
	t.Helper()
	dir, saved := t.TempDir(), cgroupRoot
	cgroupRoot = dir
	t.Cleanup(func() { cgroupRoot = saved })
	return dir
}

func TestTenantCgroupParent(t *testing.T) {
	plan := ParentCgroupFor(api.PlanPro)
	tests := []struct {
		name   string
		lease  Lease
		scopes []string // parents holding the instance's scope directory
		want   string
	}{
		{name: "launched flat", lease: Lease{Instance: "i-1", Plan: api.PlanPro, cgroupParent: CgroupMountRoot}, want: CgroupMountRoot},
		{name: "launched in its plan's sub-slice", lease: Lease{Instance: "i-1", Plan: api.PlanPro}, scopes: []string{plan}, want: plan},
		{name: "not launched yet", lease: Lease{Instance: "i-1", Plan: api.PlanPro}, want: plan},
		{name: "rebuilt from a record, found flat", lease: Lease{Instance: "i-1", Plan: api.PlanPro}, scopes: []string{CgroupMountRoot}, want: CgroupMountRoot},
		{name: "both exist: the sub-slice wins", lease: Lease{Instance: "i-1", Plan: api.PlanPro}, scopes: []string{plan, CgroupMountRoot}, want: plan},
		{name: "builder", lease: Lease{Instance: "i-1", Plan: api.PlanPro, IsBuilder: true}, scopes: []string{CgroupMountRoot}, want: BuilderCgroupParent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := withCgroupRoot(t)
			for _, parent := range tt.scopes {
				if err := os.MkdirAll(filepath.Join(root, parent, PerInstanceScope(tt.lease.Instance)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if got := vmCgroupParent(tt.lease); got != tt.want {
				t.Fatalf("vmCgroupParent = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStampCgroupParent(t *testing.T) {
	for _, tt := range []struct {
		name    string
		flat    bool
		builder bool
		want    string
	}{
		{name: "off keeps the plan's sub-slice"},
		{name: "on launches tenant VMs flat", flat: true, want: CgroupMountRoot},
		{name: "builders keep the build slice", flat: true, builder: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := (&Manager{}).WithFlatTenantCgroups(tt.flat)
			l := Lease{Instance: "i-1", Plan: api.PlanHobby, IsBuilder: tt.builder}
			m.stampCgroupParent(&l)
			if l.cgroupParent != tt.want {
				t.Fatalf("cgroupParent = %q, want %q", l.cgroupParent, tt.want)
			}
		})
	}
}

func TestJailerCommandLaunchesFlat(t *testing.T) {
	argv := JailerCommand(JailerSpec{Instance: "i-1", UID: JailUIDBase, GID: JailUIDBase, Plan: api.PlanScale, ParentCgroup: Lease{cgroupParent: CgroupMountRoot}.cgroupParent})
	i := slices.Index(argv, "--parent-cgroup")
	if i < 0 || i+1 >= len(argv) || argv[i+1] != CgroupMountRoot {
		t.Fatalf("argv = %v, want --parent-cgroup %s", argv, CgroupMountRoot)
	}
}

func TestTenantCgroupParentsIncludeTheFlatSlice(t *testing.T) {
	if parents := tenantCgroupParents(); !slices.Contains(parents, CgroupMountRoot) {
		t.Fatalf("parents = %v, want the flat tenant slice swept too", parents)
	}
}

// A journaled lease has no parent (it is not in the JSON), so a flat VM's
// in-memory lease must still match its record at cleanup.
func TestResourceLeaseMatchesIgnoresTheCgroupParent(t *testing.T) {
	recorded := Lease{Instance: "i-1", Slot: 3, Plan: api.PlanPro}
	live := recorded
	live.cgroupParent = CgroupMountRoot
	if !resourceLeaseMatches(recorded, live) {
		t.Fatal("a flat VM's lease does not match its journal record")
	}
}
