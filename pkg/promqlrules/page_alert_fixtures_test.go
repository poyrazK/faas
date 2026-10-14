package promqlrules_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pageAlertBaselineFile lists the page alerts that predate this gate and
// still have no promtool fixture that makes them fire. It may only shrink.
const pageAlertBaselineFile = "pkg/promqlrules/testdata/page_alerts_without_fixture.txt"

// TestPageAlertsHaveFiringFixture requires every severity=page alert to have
// a promtool fixture in which it fires. A page that has never been evaluated
// against samples can be dead (it matches no series) without anyone noticing
// until the incident it was written for.
func TestPageAlertsHaveFiringFixture(t *testing.T) {
	fires := firingFixtureAlerts(t)
	baseline := readPageAlertBaseline(t)

	page := map[string]bool{}
	for _, r := range loadShippedRules(t) {
		if r.Alert != "" && r.Labels["severity"] == "page" {
			page[r.Alert] = true
		}
	}

	var missing, stale []string
	for alert := range page {
		if !fires[alert] && !baseline[alert] {
			missing = append(missing, alert)
		}
	}
	for alert := range baseline {
		if !page[alert] || fires[alert] {
			stale = append(stale, alert)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, alert := range missing {
		t.Errorf("page alert %s has no promtool fixture that fires it; add an alert_rule_test with exp_alerts under pkg/promqlrules/testdata/", alert)
	}
	for _, alert := range stale {
		t.Errorf("%s is listed in %s but is now tested, renamed, removed or no longer a page; delete the line", alert, pageAlertBaselineFile)
	}
}

func readPageAlertBaseline(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRootDir, pageAlertBaselineFile))
	if err != nil {
		t.Fatalf("read %s: %v", pageAlertBaselineFile, err)
	}
	baseline := map[string]bool{}
	var prev string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line <= prev {
			t.Errorf("%s: %q is out of order or duplicated; keep the list sorted", pageAlertBaselineFile, line)
		}
		prev = line
		baseline[line] = true
	}
	return baseline
}
