//go:build metal

package fcvm

import (
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 20261109140000 — after the reseed barrier guest-init reports running
// interpreter processes the barrier missed, and vmmd counts them. A Python app
// started normally registers and must not be reported; the same app under
// `python3 -I` ignores PYTHONPATH, never loads the preload and must be
// reported, while the restore still succeeds.
//
//	FAAS_TEST_KERNEL=<release vmlinux> FAAS_TEST_RNG_PYTHON_ROOTFS=/var/tmp/python3-debian.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalReseedCoverageReport
func TestMetalReseedCoverageReport(t *testing.T) {
	tarball := os.Getenv("FAAS_TEST_RNG_PYTHON_ROOTFS")
	if tarball == "" {
		t.Skip("set FAAS_TEST_RNG_PYTHON_ROOTFS to a Python runner root filesystem tarball")
	}
	for _, arm := range []struct {
		name       string
		entrypoint []string
		reported   float64
	}{
		{"registered", []string{"/usr/bin/python3", "/srv/rng-app.py"}, 0},
		{"isolated-mode", []string{"/usr/bin/python3", "-I", "/srv/rng-app.py"}, 1},
	} {
		t.Run(arm.name, func(t *testing.T) {
			metrics := NewWakePhaseMetrics()
			p, _ := startCompatProbe(t, "reseed-"+arm.name, tarball, "srv/rng-app.py", rngPythonApp, arm.entrypoint, map[string]string{},
				func(v *JailerVMM) { v.WithWakePhaseMetrics(metrics) })
			p.park()
			got := p.getJSON(p.restore(), "/")
			reported := testutil.ToFloat64(metrics.reseedUnregistered.WithLabelValues("python", "workload"))
			t.Logf("%s: restored probe %v; python processes reported unreseeded: %v", arm.name, got, reported)
			if reported != arm.reported {
				t.Fatalf("vmmd_restore_reseed_unregistered_total{runtime=python} = %v, want %v", reported, arm.reported)
			}
		})
	}
}
