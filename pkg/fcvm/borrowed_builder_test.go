package fcvm

// adr: 20261118154632 — builders on borrowed slots run in faas-borrowed.slice.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuilderCgroupParent(t *testing.T) {
	tests := []struct {
		name         string
		lease        Lease
		scopes       []string
		want         string
		wantBorrowed bool
	}{
		{name: "reserved builder", lease: Lease{Instance: "b-1", IsBuilder: true}, want: BuilderCgroupParent},
		{name: "borrowed at launch", lease: Lease{Instance: "b-1", IsBuilder: true, cgroupParent: BorrowedBuilderCgroupParent}, want: BorrowedBuilderCgroupParent, wantBorrowed: true},
		{name: "rebuilt from a record, found borrowed", lease: Lease{Instance: "b-1", IsBuilder: true}, scopes: []string{BorrowedBuilderCgroupParent}, want: BorrowedBuilderCgroupParent, wantBorrowed: true},
		{name: "both exist: the reserved slice wins", lease: Lease{Instance: "b-1", IsBuilder: true}, scopes: []string{BuilderCgroupParent, BorrowedBuilderCgroupParent}, want: BuilderCgroupParent},
		{name: "an app is never borrowed", lease: Lease{Instance: "b-1", Plan: api.PlanPro}, scopes: []string{BorrowedBuilderCgroupParent}, want: ParentCgroupFor(api.PlanPro)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := withCgroupRoot(t)
			for _, parent := range tt.scopes {
				if err := os.MkdirAll(filepath.Join(root, parent, tt.lease.Instance), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if got := vmCgroupParent(tt.lease); got != tt.want {
				t.Fatalf("vmCgroupParent = %q, want %q", got, tt.want)
			}
			if got := tt.lease.BorrowedBuilder(); got != tt.wantBorrowed {
				t.Fatalf("BorrowedBuilder = %t, want %t", got, tt.wantBorrowed)
			}
		})
	}
}

func TestJailerCommandBorrowedBuilderParent(t *testing.T) {
	parent := func(args []string) string {
		i := slices.Index(args, "--parent-cgroup")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("no --parent-cgroup in %v", args)
		}
		return args[i+1]
	}
	reserved := JailerCommand(JailerSpec{Instance: "b-1", UID: JailUIDBase, GID: JailUIDBase, IsBuilder: true})
	if got := parent(reserved); got != BuilderCgroupParent {
		t.Fatalf("reserved builder parent = %q, want %q", got, BuilderCgroupParent)
	}
	borrowed := JailerCommand(JailerSpec{Instance: "b-1", UID: JailUIDBase, GID: JailUIDBase, IsBuilder: true, ParentCgroup: BorrowedBuilderCgroupParent})
	if got := parent(borrowed); got != BorrowedBuilderCgroupParent {
		t.Fatalf("borrowed builder parent = %q, want %q", got, BorrowedBuilderCgroupParent)
	}
	if BorrowedBuilderCgroupParent != "faas.slice/faas-borrowed.slice" {
		t.Fatalf("BorrowedBuilderCgroupParent = %q; the systemd unit is faas-borrowed.slice", BorrowedBuilderCgroupParent)
	}
}

func TestWriteBuildCgroupInBorrowedSlice(t *testing.T) {
	root := withCgroupRoot(t)
	for _, parent := range []string{BuilderCgroupParent, BorrowedBuilderCgroupParent} {
		scope := filepath.Join(root, parent, "b-1")
		if err := os.MkdirAll(scope, 0o755); err != nil {
			t.Fatal(err)
		}
		// ensureSubtreeControllers reads the parent's controllers.
		if err := os.WriteFile(filepath.Join(root, parent, "cgroup.controllers"), []byte("cpu memory pids\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, parent, "cgroup.subtree_control"), []byte("cpu memory pids\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeBuildCgroup(BorrowedBuilderCgroupParent, "b-1", api.BuildVMRAMMB); err != nil {
		t.Fatal(err)
	}
	cpuMax, err := os.ReadFile(filepath.Join(root, BorrowedBuilderCgroupParent, "b-1", "cpu.max"))
	if err != nil || strings.TrimSpace(string(cpuMax)) != builderCPUMax {
		t.Fatalf("borrowed builder cpu.max = %q, %v; want %q", cpuMax, err, builderCPUMax)
	}
	if _, err := os.Stat(filepath.Join(root, BuilderCgroupParent, "b-1", "cpu.max")); !os.IsNotExist(err) {
		t.Fatalf("the reserved slice's scope was written: %v", err)
	}
}

func TestWriteOOMScoreAdj(t *testing.T) {
	proc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proc, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeOOMScoreAdj(proc, 42, api.BorrowedBuilderOOMScoreAdj); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(proc, "42", "oom_score_adj"))
	if err != nil || strings.TrimSpace(string(got)) != "1000" {
		t.Fatalf("oom_score_adj = %q, %v; want 1000", got, err)
	}
	for _, c := range []struct{ pid, score int }{{0, 1000}, {42, 1001}, {42, -1001}} {
		if err := writeOOMScoreAdj(proc, c.pid, c.score); err == nil {
			t.Fatalf("writeOOMScoreAdj(%d, %d) accepted", c.pid, c.score)
		}
	}
}

func TestNativeParentPlanKnowsBorrowedBuilders(t *testing.T) {
	plan, builder, owned := nativeParentPlan("/" + BorrowedBuilderCgroupParent)
	if plan != "" || !builder || !owned {
		t.Fatalf("nativeParentPlan(borrowed) = %q, %t, %t; want a managed builder", plan, builder, owned)
	}
}
