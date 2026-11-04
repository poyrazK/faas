// Synthetic-fixture driver for the Postgres pool alerts, FaasDBPoolStarved
// and FaasDBPoolNearCap. Invokes `promtool test rules` against the two
// fixtures that hold their cases and asserts the exit code is 0.
//
// What this pins: a daemon may run more than one pool. apid's background
// loops got their own (#6464), exported as apid_background_db_pool_*. The
// starvation rule rated {__name__=~".+_db_pool_canceled_acquires_total"};
// rate() drops the metric name, so apid's two counters collided and the rule
// failed on every evaluation, for every daemon (production-us rc.319,
// 2026-11-04: "vector cannot contain metrics with the same labelset"). The
// near-cap rule summed both pools against the larger cap. Both fixtures carry
// a two-pool case that fails against those rules.
//
// Build tag: integration, as cpu_starvation_test.go: promtool may not be
// installed on the dev box; CI installs it.

//go:build integration

package promqlrules_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDBPoolAlertsHoldEachPoolApart(t *testing.T) {
	if _, err := exec.LookPath("promtool"); err != nil {
		t.Skip("promtool not installed on PATH; CI's promtool test rules step runs these fixtures unconditionally")
	}
	for _, name := range []string{"core_page_alerts.test.yml", "spec12_operator_signals.test.yml"} {
		fixture, err := filepath.Abs(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		out, err := exec.Command("promtool", "test", "rules", fixture).CombinedOutput()
		if err != nil {
			t.Fatalf("promtool test rules failed for %s:\n%s\n%v", fixture, out, err)
		}
	}
}
