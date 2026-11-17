package promqlrules_test

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// lvFcAlertsN counts the warn and page tiers.
const lvFcAlertsN = 2

var (
	lvFcAbove  = regexp.MustCompile(`max\(fcvm_lv_fc_used_pct\) > ([0-9.]+)`)
	lvFcAtMost = regexp.MustCompile(`max\(fcvm_lv_fc_used_pct\) <= ([0-9.]+)`)
)

// The artifact cache fills lv-fc up to its free-space floor, 100 minus
// api.ArtifactCacheReservePercent, so a busy node sits there by design
// (ADR-20261114182830). A warn at or below that floor fired on every healthy
// node (it was 80 against an 85 % floor). The warn must sit above the floor
// and below the page, and the page must match the level at which imaged's GC
// switches to fleet budget pressure (api.SnapshotBudgetAlarmPct).
func TestLvFcAlertsSitAboveTheArtifactCacheFloor(t *testing.T) {
	var warnLow, warnHigh, page float64
	found := 0
	for _, r := range loadShippedRules(t) {
		switch r.Alert {
		case "FaasLvFcUsageHighWarn":
			warnLow = lvFcThreshold(t, r.Alert, lvFcAbove, r.Expr)
			warnHigh = lvFcThreshold(t, r.Alert, lvFcAtMost, r.Expr)
			found++
		case "FaasLvFcUsageHighPage":
			page = lvFcThreshold(t, r.Alert, lvFcAbove, r.Expr)
			found++
		}
	}
	if found != lvFcAlertsN {
		t.Fatalf("found %d of the %d lv-fc usage alerts", found, lvFcAlertsN)
	}
	floor := float64(100 - api.ArtifactCacheReservePercent)
	if warnLow <= floor {
		t.Errorf("FaasLvFcUsageHighWarn fires above %v%%, at or below the artifact cache floor %v%%; it would warn on every healthy node", warnLow, floor)
	}
	if warnLow >= page {
		t.Errorf("FaasLvFcUsageHighWarn lower bound %v%% is not below the page threshold %v%%", warnLow, page)
	}
	if warnHigh != page {
		t.Errorf("FaasLvFcUsageHighWarn upper bound %v%% differs from the page threshold %v%%", warnHigh, page)
	}
	if page != api.SnapshotBudgetAlarmPct {
		t.Errorf("FaasLvFcUsageHighPage threshold %v%% differs from api.SnapshotBudgetAlarmPct %v%%", page, api.SnapshotBudgetAlarmPct)
	}
}

func lvFcThreshold(t *testing.T, alert string, re *regexp.Regexp, expr string) float64 {
	t.Helper()
	m := re.FindStringSubmatch(expr)
	if m == nil {
		t.Fatalf("%s expr %q has no %s threshold", alert, expr, re)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("%s threshold %q: %v", alert, m[1], err)
	}
	return v
}
