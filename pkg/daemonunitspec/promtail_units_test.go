package daemonunitspec

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// promtailUnshippedUnits are faas units whose journal is deliberately not
// sent to Loki. Each needs a reason.
var promtailUnshippedUnits = map[string]string{
	"faas-github-runner.service": "CI runner: job output belongs to GitHub, and the runner is not part of the platform",
}

// TestPromtailShipsEveryFaasUnit pins ADR-20261023124955 §2. The Promtail
// allowlist named 13 units while the platform had grown to more than 30;
// realtimed, outboundd, bridged, profiled, livequeryd, maild and others
// never reached Loki. Every faas-*.service the deploy tree installs must be
// in alloy_journal_units, the shipper's list since ADR-20261023185953, or
// carry a reason in promtailUnshippedUnits. While Promtail stays selectable
// as the rollback, its list must be the same.
func TestPromtailShipsEveryFaasUnit(t *testing.T) {
	root := repoRoot(t)
	roles := filepath.Join(root, "deploy", "ansible", "roles")
	raw, _ := readRoleDefaults(t, filepath.Join(roles, "alloy"))["alloy_journal_units"].([]any)
	shipped := map[string]bool{}
	var alloyList []string
	for _, u := range raw {
		name, _ := u.(string)
		if shipped[name] {
			t.Errorf("alloy_journal_units lists %s twice", name)
		}
		shipped[name] = true
		alloyList = append(alloyList, name)
	}
	var promtailList []string
	rawPromtail, _ := readRoleDefaults(t, filepath.Join(roles, "promtail"))["promtail_journal_units"].([]any)
	for _, u := range rawPromtail {
		name, _ := u.(string)
		promtailList = append(promtailList, name)
	}
	if strings.Join(alloyList, ",") != strings.Join(promtailList, ",") {
		t.Errorf("promtail_journal_units differs from alloy_journal_units; the rollback shipper must ship the same units:\n alloy:    %v\n promtail: %v", alloyList, promtailList)
	}

	installed := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(d.Name(), ".j2")
		if !d.IsDir() && strings.HasPrefix(name, "faas-") && strings.HasSuffix(name, ".service") {
			installed[name] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) < 20 {
		t.Fatalf("found only %d faas units under deploy/; the walk is broken", len(installed))
	}

	var missing []string
	for unit := range installed {
		if !shipped[unit] && promtailUnshippedUnits[unit] == "" {
			missing = append(missing, unit)
		}
	}
	sort.Strings(missing)
	for _, unit := range missing {
		t.Errorf("%s is installed but not in alloy_journal_units; ship it or add a reason to promtailUnshippedUnits", unit)
	}
	for unit := range promtailUnshippedUnits {
		if shipped[unit] {
			t.Errorf("%s is both shipped and excluded", unit)
		}
		if !installed[unit] {
			t.Errorf("promtailUnshippedUnits lists %s, which no longer exists under deploy/; delete the entry", unit)
		}
	}
	for unit := range shipped {
		if strings.HasPrefix(unit, "faas-") && !installed[unit] {
			t.Errorf("alloy_journal_units lists %s, which no file under deploy/ installs", unit)
		}
	}
}
