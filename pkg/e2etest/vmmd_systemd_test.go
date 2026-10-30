package e2etest

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// In FAAS_E2E_VMMD_SYSTEMD mode the harness's vmmd carries the production
// unit's sandbox, including the private mount namespace ProtectSystem gives it,
// so a bug that only shows under systemd fails metal tests too.
func TestVMMDSandboxPropertiesFollowTheProductionUnit(t *testing.T) {
	unit, err := daemonunitspec.UnitByName("vmmd")
	if err != nil {
		t.Fatal(err)
	}
	props := vmmdSandboxProperties(unit.Render(), []string{"-/mnt/e2e/run"})
	want := map[string]bool{"ProtectSystem=strict": false, "NoNewPrivileges=yes": false}
	var rw, caps string
	for _, p := range props {
		if _, ok := want[p]; ok {
			want[p] = true
		}
		if v, ok := strings.CutPrefix(p, "ReadWritePaths="); ok {
			rw = v
		}
		if v, ok := strings.CutPrefix(p, "CapabilityBoundingSet="); ok {
			caps = v
		}
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("sandbox properties %v lack %s", props, p)
		}
	}
	paths := strings.Fields(rw)
	for _, p := range []string{"/run/netns", "/srv/fc", "-/mnt/e2e/run"} {
		if !slices.Contains(paths, p) {
			t.Errorf("ReadWritePaths %q lack %s", rw, p)
		}
	}
	if !strings.Contains(caps, "cap_sys_admin") || strings.Contains(caps, "cap_sys_module") {
		t.Errorf("CapabilityBoundingSet = %q, want the production set", caps)
	}
}

func TestHarnessWritablePathsKeepTheHostTreeReadOnly(t *testing.T) {
	dir := t.TempDir()
	got := harnessWritablePaths([]string{"FAAS_A=" + dir, "FAAS_SOCK=" + dir + "/vmmd.sock", "PATH=/usr/bin", "FAAS_ETC=/etc/faas", "FAAS_REL=relative"})
	if !slices.Contains(got, "-"+dir) {
		t.Errorf("writable %v lack the harness directory %s", got, dir)
	}
	for _, p := range got {
		for _, root := range hostTreeRoots {
			if p == "-"+root || strings.HasPrefix(p, "-"+root+"/") {
				t.Errorf("writable %v include host tree %s", got, p)
			}
		}
	}
}

func TestVMMDUnitOfFindsOnlyTheWrapper(t *testing.T) {
	wrapped := &exec.Cmd{Path: "/usr/bin/systemd-run", Args: []string{"systemd-run", "--wait", "--unit=" + vmmdUnitPrefix + "ab12.service", "--", "/bin/vmmd"}}
	if vmmdUnitOf(wrapped) != vmmdUnitPrefix+"ab12.service" || !isVMMDProc(wrapped) {
		t.Fatalf("wrapper not recognised: %q", vmmdUnitOf(wrapped))
	}
	direct := &exec.Cmd{Path: "/srv/bin/vmmd", Args: []string{"/srv/bin/vmmd"}}
	if vmmdUnitOf(direct) != "" || !isVMMDProc(direct) {
		t.Fatal("direct vmmd not recognised")
	}
	// A --unit= after "--" is vmmd's own argument, not the wrapper's.
	other := &exec.Cmd{Path: "/srv/bin/schedd", Args: []string{"/srv/bin/schedd", "--", "--unit=" + vmmdUnitPrefix + "x"}}
	if isVMMDProc(other) {
		t.Fatal("schedd taken for vmmd")
	}
}
