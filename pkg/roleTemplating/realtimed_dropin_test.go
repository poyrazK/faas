package roleTemplating

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The release install writes DropInFileName for every daemon after the
// gatewayd_internal_service role runs on a fresh join. The role's realtimed
// drop-in carries the compute-only health listener and apid history target,
// so it must not share that file name, and it must sort after it so its
// settings win (OVH cutover, 2026-11-20: realtimed listened on loopback only).
func TestRealtimedRoleDropInSurvivesReleaseInstall(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "gatewayd_internal_service", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)src: 99-faas-realtimed-role\.conf\.j2\n\s+dest: (\S+)`).FindSubmatch(body)
	if m == nil {
		t.Fatal("gatewayd_internal_service no longer installs 99-faas-realtimed-role.conf.j2")
	}
	dest := string(m[1])
	if filepath.Dir(dest) != DropInDir("realtimed") {
		t.Fatalf("realtimed role drop-in %s is outside %s", dest, DropInDir("realtimed"))
	}
	name := filepath.Base(dest)
	if name == DropInFileName {
		t.Fatalf("realtimed role drop-in shares %s with the release install, which replaces it", DropInFileName)
	}
	if name <= DropInFileName {
		t.Fatalf("realtimed role drop-in %s sorts before %s, so the install's settings would win", name, DropInFileName)
	}
}
