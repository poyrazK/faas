package daemonunitspec

import (
	"strconv"
	"testing"
	"time"
)

// TestUnitVmmd_RetryCadenceSurvivesControlPlaneBoot pins the recovery
// contract from issue #2093. systemd's common default allows five starts in a
// ten-second window. A two-second cadence can consume that whole budget while
// the control-plane PostgreSQL service is still replaying WAL, after which a
// healthy database cannot bring vmmd back without operator intervention.
func TestUnitVmmd_RetryCadenceSurvivesControlPlaneBoot(t *testing.T) {
	u := UnitVmmd()
	restartDelay, err := time.ParseDuration(u.RestartSec)
	if err != nil {
		t.Fatalf("parse vmmd RestartSec %q: %v", u.RestartSec, err)
	}
	startLimitWindow, err := time.ParseDuration(u.StartLimitIntervalSec)
	if err != nil {
		t.Fatalf("parse vmmd StartLimitIntervalSec %q: %v", u.StartLimitIntervalSec, err)
	}
	startLimitBurst, err := strconv.Atoi(u.StartLimitBurst)
	if err != nil {
		t.Fatalf("parse vmmd StartLimitBurst %q: %v", u.StartLimitBurst, err)
	}
	if retryCoverage := time.Duration(startLimitBurst) * restartDelay; retryCoverage < startLimitWindow {
		t.Fatalf("vmmd retries exhaust systemd's start limit: %d starts x %s covers %s, below the %s window",
			startLimitBurst, restartDelay, retryCoverage, startLimitWindow)
	}
}
