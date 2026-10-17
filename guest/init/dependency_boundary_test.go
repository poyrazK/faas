package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// spec: §4.6
// guest-init is PID 1 in every microVM and ships in every app layer. A probe
// that imported pkg/outbound for one header constant linked the control plane
// (schedd state, billing, apid) into it: 1,096 packages and a 48 MB binary
// (production-us hunt #10, H10-139). The rule is transitive, which depguard's
// direct-import rules cannot express.
func TestGuestInitLinksNoControlPlanePackages(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	cmd := exec.Command(goBin, "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	forbidden := []string{"pkg/apid", "pkg/billing", "pkg/db", "pkg/gateway", "pkg/outbound", "pkg/sched", "pkg/state", "pkg/wire"}
	// Standard-library-only leaves that pkg/api shares with the guest.
	allowed := map[string]bool{"pkg/outbound/routepolicy": true}
	for _, dep := range strings.Fields(string(out)) {
		rel, ok := strings.CutPrefix(dep, "github.com/onebox-faas/faas/")
		if !ok || allowed[rel] {
			continue
		}
		for _, f := range forbidden {
			if rel == f || strings.HasPrefix(rel, f+"/") {
				t.Errorf("guest-init links %s; keep control-plane packages out of PID 1", rel)
			}
		}
	}
}
