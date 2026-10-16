package obsrulecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecordingCatalogDriftAndReferences(t *testing.T) {
	raw, err := GenerateRecordings(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, recordingJSON) {
		t.Fatal("recording catalog drift; run make obs-rule-catalog-generate")
	}
	c, err := LoadRecordings()
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range c.Sets {
		if len(set.Expected.Rules) < 20 || len(set.Dependents) != len(set.Expected.Rules) || set.AlertCount < 200 {
			t.Fatal("incomplete recording catalog")
		}
		found := false
		for i, rule := range set.Expected.Rules {
			if rule.RuleName != "faas_compute_metrics_healthy_targets:by_job" {
				continue
			}
			for _, ref := range set.Dependents[i].Alerts {
				if ref.RuleName == "FaasComputeMetricsScrapeCoverageLow" {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("transitive compute-coverage alert dependency missing")
		}
	}
	c.Sets[0].Expected.Files[0] = "mutated"
	c.Sets[0].Dependents[0].Alerts = append(c.Sets[0].Dependents[0].Alerts, RuleReference{RuleName: "Mutated"})
	fresh, err := LoadRecordings()
	if err != nil || fresh.Sets[0].Expected.Files[0] == "mutated" {
		t.Fatal("shared mutable recording catalog")
	}
}

func recordingSource(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	for i, rel := range sourceFiles {
		file := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		content := "groups: []\n"
		if i == 0 {
			content = body
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRecordingDependencyChainsAndUncertainty(t *testing.T) {
	body := `groups:
- name: chain
  rules:
  - record: stage:a
    expr: raw_metric
  - record: stage:b
    expr: stage:a + 1
  - alert: ExampleChain
    expr: stage:b > 0
  - alert: ExampleDirect
    expr: '{__name__="stage:a"} > 0'
  - alert: ExampleDynamic
    expr: '{__name__=~"stage:.*"} > 0'
- name: ambiguous
  rules:
  - record: stage:a
    expr: other_raw_metric
  - alert: ExampleAmbiguous
    expr: stage:a > 0
- name: cycles
  rules:
  - record: cycle:a
    expr: cycle:b
  - record: cycle:b
    expr: cycle:a
  - alert: ExampleCycle
    expr: cycle:a > 0
`
	root := recordingSource(t, body)
	raw, err := GenerateRecordings(root)
	if err != nil {
		t.Fatal(err)
	}
	alertRaw, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	alerts, err := decode(alertRaw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeRecordings(raw, alerts)
	if err != nil {
		t.Fatal(err)
	}
	set := c.Sets[0]
	if len(set.UncertainAlerts) != 5 {
		t.Fatalf("ambiguous, dynamic and cyclic mappings must be explicit: %+v", set.UncertainAlerts)
	}
	for i, rule := range set.Expected.Rules {
		refs := set.Dependents[i].Alerts
		if rule.RuleName == "stage:a" && len(refs) != 0 {
			t.Fatal("ambiguous producers selected arbitrarily")
		}
		if rule.RuleName == "stage:b" && (len(refs) != 1 || refs[0].RuleName != "ExampleChain") {
			t.Fatal("known syntactic edge lost despite uncertainty upstream")
		}
	}
	// Removing the duplicate producer makes both the direct and transitive
	// references known; regex selectors and cycles stay uncertain.
	body = strings.Replace(body, "  - record: stage:a\n    expr: other_raw_metric\n", "", 1)
	if err := os.WriteFile(filepath.Join(root, sourceFiles[0]), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err = GenerateRecordings(root)
	if err != nil {
		t.Fatal(err)
	}
	var updated RecordingCatalog
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatal(err)
	}
	set = updated.Sets[0]
	if len(set.UncertainAlerts) != 2 {
		t.Fatal("resolved producer ambiguity retained uncertainty")
	}
	for i, rule := range set.Expected.Rules {
		if rule.RuleName == "stage:a" && len(set.Dependents[i].Alerts) != 3 {
			t.Fatal("transitive alert references missing")
		}
	}
}

func TestRecordingCatalogRejectsCorruption(t *testing.T) {
	alerts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RecordingCatalog){
		func(c *RecordingCatalog) { c.SchemaVersion++ },
		func(c *RecordingCatalog) { c.Sets[0].Revision = strings.Repeat("0", 64) },
		func(c *RecordingCatalog) {
			s := &c.Sets[0]
			s.AlertSetRevision = strings.Repeat("0", 64)
			s.Revision = recordingRevision(*s)
		},
		func(c *RecordingCatalog) {
			s := &c.Sets[0]
			s.Expected.Rules[0].Definition.ForSeconds = 1
			s.Expected.Rules[0].Definition.Fingerprint = definitionFingerprint(s.Expected.Rules[0].Definition)
			s.Expected.Revision = revision(s.Expected)
			s.Revision = recordingRevision(*s)
		},
		func(c *RecordingCatalog) {
			s := &c.Sets[0]
			s.Dependents[0].Alerts = []RuleReference{{ID: strings.Repeat("0", 64), RuleName: "Unknown"}}
			s.Revision = recordingRevision(*s)
		},
		func(c *RecordingCatalog) {
			s := &c.Sets[0]
			s.UncertainAlerts = append(s.UncertainAlerts, s.UncertainAlerts[0])
			s.Revision = recordingRevision(*s)
		},
	} {
		c, err := LoadRecordings()
		if err != nil {
			t.Fatal(err)
		}
		change(&c)
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRecordings(raw, alerts); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatal("corrupt dependency evidence accepted")
		}
	}
	if _, err := decodeRecordings(append(bytes.Clone(recordingJSON), []byte(" {}")...), alerts); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatal("trailing recording catalog accepted")
	}
}

func TestRecordingDependencyProfilesAreIndependent(t *testing.T) {
	core := "groups:\n- name: core\n  rules:\n  - record: stage:a\n    expr: raw_metric\n  - alert: ExampleCore\n    expr: stage:a > 0\n"
	root := recordingSource(t, core)
	companion := "groups:\n- name: companion\n  rules:\n  - record: stage:a\n    expr: other_raw_metric\n  - alert: ExampleCompanion\n    expr: stage:a > 0\n"
	if err := os.WriteFile(filepath.Join(root, sourceFiles[1]), []byte(companion), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := GenerateRecordings(root)
	if err != nil {
		t.Fatal(err)
	}
	var c RecordingCatalog
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Sets[0].Expected.ID != CoreSetID || len(c.Sets[0].Expected.Rules) != 1 || len(c.Sets[0].UncertainAlerts) != 0 || len(c.Sets[0].Dependents[0].Alerts) != 1 {
		t.Fatal("core profile lost its complete exact reference due to an unselected companion")
	}
	if c.Sets[1].Expected.ID != FullSetID || len(c.Sets[1].Expected.Rules) != 2 || len(c.Sets[1].UncertainAlerts) != 2 {
		t.Fatal("full profile hid duplicate producer uncertainty")
	}
	for _, dep := range c.Sets[1].Dependents {
		if len(dep.Alerts) != 0 {
			t.Fatal("full profile chose an ambiguous producer")
		}
	}
}

func TestRecordingGeneratorLimitsAndUnsupportedTimers(t *testing.T) {
	body := "groups:\n- name: example\n  rules:\n  - record: stage:a\n    expr: up\n    for: 1m\n  - alert: Example\n    expr: stage:a > 0\n"
	if _, err := GenerateRecordings(recordingSource(t, body)); err == nil {
		t.Fatal("recording timer silently ignored")
	}
	var b strings.Builder
	b.WriteString("groups:\n- name: graph\n  rules:\n")
	for i := 0; i < 200; i++ {
		input := "raw_metric"
		if i > 0 {
			input = fmt.Sprintf("stage:r%d", i-1)
		}
		fmt.Fprintf(&b, "  - record: stage:r%d\n    expr: %s\n", i, input)
	}
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "  - alert: Example%d\n    expr: stage:r199 > 0\n", i)
	}
	if _, err := GenerateRecordings(recordingSource(t, b.String())); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("more than %d dependency links accepted: %v", api.ObsRecordingDependencyLinksMax, err)
	}
}
