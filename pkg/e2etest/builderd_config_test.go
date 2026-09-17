package e2etest

import (
	"strings"
	"testing"
)

// TestBuilderdConfigSpoolRootMatchesAPID — builderd must validate source paths
// against the same spool root apid writes them to.
//
// spec: §4.5
// adr: 003
//
// apid spools uploaded tarballs to FAAS_SPOOL_ROOT (per-test, under TmpDir).
// builderd checks every source path against its own configured root and
// refuses anything outside it. When the per-test builderd.toml omitted
// source_spool_dir, builderd kept the production default and rejected every
// upload on the 2026-09-14 native run:
//
//	builderd: source boundary violation: path ".../spool/x.tar.gz"
//	  is outside spool root "/var/spool/faas/builds"
//
// failure_class=infra, deployments stuck at status=pending, wakes 503.
func TestBuilderdConfigSpoolRootMatchesAPID(t *testing.T) {
	const tmp = "/tmp/harness-probe"
	cfg := builderdConfig(tmp, "/run/faas/vmmd.sock", "/srv/fc/base/builder-base.ext4", "127.0.0.1:9105")

	want := `source_spool_dir = "` + spoolRootFor(tmp) + `"`
	if !strings.Contains(cfg, want) {
		t.Fatalf("builderd.toml does not pin the spool root to apid's.\nwant line: %s\ngot:\n%s", want, cfg)
	}

	// The production default must never survive into a per-test config: that is
	// the exact value that made the guard fire.
	if strings.Contains(cfg, "/var/spool/faas/builds") {
		t.Error("builderd.toml still references the production spool root")
	}

	// Everything else the per-test config redirects must stay redirected, so a
	// future edit cannot quietly point a daemon at a shared host directory.
	for _, key := range []string{"cache_dir", "build_drive_dir", "build_export_dir", "source_spool_dir"} {
		line := key + ` = "` + tmp
		if !strings.Contains(cfg, line) {
			t.Errorf("%s is not redirected under the test temp dir:\n%s", key, cfg)
		}
	}
}
