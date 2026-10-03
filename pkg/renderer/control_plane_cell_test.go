package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestControlPlaneCellManifestRender(t *testing.T) {
	file := fixtureManifest(t, "cell-local", "single-box")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cell := &manifest.ControlPlaneCell{ID: "01a0f8a4-1808-7f20-898a-eabdd349b0ec", Region: "eu-fsn"}
	yaml := strings.Replace(string(body), "fleet:\n", "control_plane_cell:\n  id: "+cell.ID+"\n  region: "+cell.Region+"\nfleet:\n", 1)
	yaml = strings.Replace(yaml, "      role: single-box\n", "      role: single-box\n      region: "+cell.Region+"\n", 1)
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opts := RenderOptions{ManifestPath: file, ReleasesRoot: filepath.Join(dir, "releases"), EtcFaasDir: filepath.Join(dir, "etc"),
		SystemdDir: filepath.Join(dir, "units"), PKIRootDir: filepath.Join(dir, "tls"), CgroupRoot: filepath.Join(dir, "cgroup")}
	for range 2 {
		if _, err := Render(opts); err != nil {
			t.Fatal(err)
		}
	}
	for _, daemon := range daemonunitspec.ActivationOrder() {
		unit, err := os.ReadFile(filepath.Join(opts.SystemdDir, "faas-"+daemon+".service"))
		if err != nil || !strings.Contains(string(unit), "Environment=FAAS_CONTROL_PLANE_CELL_ID="+cell.ID) ||
			!strings.Contains(string(unit), "Environment=FAAS_CONTROL_PLANE_CELL_REGION="+cell.Region) {
			t.Fatalf("rendered %s lost cell identity: %v", daemon, err)
		}
	}
}

func TestControlPlaneCellOnEveryDaemonUnit(t *testing.T) {
	cell := &manifest.ControlPlaneCell{ID: "01a0f8a4-1808-7f20-898a-eabdd349b0ec", Region: "eu-fsn"}
	for _, role := range []daemonunitspec.Role{daemonunitspec.RoleControlPlane, daemonunitspec.RoleComputeOnly} {
		for _, daemon := range daemonunitspec.DaemonsForRole(role) {
			body, err := renderCellSystemd(daemon, "", cell)
			if err != nil {
				t.Fatal(err)
			}
			for _, env := range []string{"Environment=FAAS_CONTROL_PLANE_CELL_ID=" + cell.ID, "Environment=FAAS_CONTROL_PLANE_CELL_REGION=" + cell.Region} {
				if strings.Count(string(body), env) != 1 {
					t.Fatalf("%s/%s missing unique identity: %s", role, daemon, env)
				}
			}
			legacy, err := renderCellSystemd(daemon, "", nil)
			if err != nil || strings.Contains(string(legacy), "FAAS_CONTROL_PLANE_CELL_") {
				t.Fatalf("legacy unit changed: %s err=%v", daemon, err)
			}
		}
	}
}
