package promqlrules_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoRootDir is the repository root relative to this package.
var repoRootDir = filepath.Join("..", "..")

// shippedRuleFiles are the rule files Prometheus loads in production; the
// list mirrors scripts/ci/check_prometheus_alert_metadata.sh.
var shippedRuleFiles = []string{
	"deploy/ansible/roles/prometheus/files/faas.rules.yml",
	"deploy/ansible/roles/prometheus/files/bridge.rules.yml",
	"deploy/ansible/roles/prometheus/files/pg_backup.rules.yml",
	"deploy/ansible/roles/prometheus/files/data_repair.rules.yml",
	"pkg/promqlrules/data_placement.yaml",
}

// ruleFixtureGlobs match every promtool fixture CI runs (ci.yml's
// "promtool check rules" step).
var ruleFixtureGlobs = []string{
	"deploy/ansible/roles/prometheus/files/*.test.yml",
	"pkg/promqlrules/testdata/*.test.yml",
}

type shippedRule struct {
	File   string
	Alert  string
	Record string
	Expr   string
	Labels map[string]string
}

func loadShippedRules(t *testing.T) []shippedRule {
	t.Helper()
	var rules []shippedRule
	for _, rel := range shippedRuleFiles {
		raw, err := os.ReadFile(filepath.Join(repoRootDir, rel))
		if err != nil {
			t.Fatalf("read rule file %s: %v", rel, err)
		}
		var doc struct {
			Groups []struct {
				Rules []struct {
					Alert  string            `yaml:"alert"`
					Record string            `yaml:"record"`
					Expr   string            `yaml:"expr"`
					Labels map[string]string `yaml:"labels"`
				} `yaml:"rules"`
			} `yaml:"groups"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse rule file %s: %v", rel, err)
		}
		for _, g := range doc.Groups {
			for _, r := range g.Rules {
				rules = append(rules, shippedRule{File: rel, Alert: r.Alert, Record: r.Record, Expr: r.Expr, Labels: r.Labels})
			}
		}
	}
	if len(rules) == 0 {
		t.Fatal("no rules loaded from the shipped rule files")
	}
	return rules
}

// firingFixtureAlerts returns the alerts that at least one promtool fixture
// expects to fire. A fixture that only asserts exp_alerts: [] proves the
// alert stays quiet, not that it can fire, so it does not count.
func firingFixtureAlerts(t *testing.T) map[string]bool {
	t.Helper()
	fires := map[string]bool{}
	var files []string
	for _, glob := range ruleFixtureGlobs {
		matches, err := filepath.Glob(filepath.Join(repoRootDir, glob))
		if err != nil {
			t.Fatalf("glob %s: %v", glob, err)
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no promtool fixtures found")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture %s: %v", path, err)
		}
		var doc struct {
			Tests []struct {
				AlertRuleTest []struct {
					Alertname string           `yaml:"alertname"`
					ExpAlerts []map[string]any `yaml:"exp_alerts"`
				} `yaml:"alert_rule_test"`
			} `yaml:"tests"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse fixture %s: %v", path, err)
		}
		for _, tc := range doc.Tests {
			for _, a := range tc.AlertRuleTest {
				if len(a.ExpAlerts) > 0 {
					fires[a.Alertname] = true
				}
			}
		}
	}
	return fires
}
