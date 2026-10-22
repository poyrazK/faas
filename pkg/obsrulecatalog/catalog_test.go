package obsrulecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeDefinition(t *testing.T) {
	queries := []string{
		`sum by (zone, job) (rate(requests_total{zone="a",job="b"}[5m])) / on (zone, job) group_left (team, owner) capacity`,
		`sum by(job,zone)(rate(requests_total{job="b", zone="a"}[300s])) / on(job,zone) group_left(owner,team) capacity`,
	}
	a, err := Normalize(queries[0], 60, 30, map[string]string{"severity": "warning", "team": "ops"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Normalize(queries[1], 60, 30, map[string]string{"team": "ops", "severity": "warning"})
	if err != nil || a != b || len(a.ChangedFields(b)) != 0 {
		t.Fatalf("formatting or label order changed definition: %+v %+v %v", a, b, err)
	}
	b, err = Normalize(`up == 0`, 61, 31, map[string]string{"severity": "critical"})
	if err != nil || !reflect.DeepEqual(a.ChangedFields(b), []string{"expression", "for", "keep_firing_for", "labels"}) {
		t.Fatalf("lost changed fields: %+v %v", a.ChangedFields(b), err)
	}
	a, _ = Normalize("up", 0, 0, nil)
	b, _ = Normalize("up", math.Copysign(0, -1), math.Copysign(0, -1), map[string]string{})
	if a != b {
		t.Fatal("zero or empty labels changed fingerprint")
	}
	for _, tc := range []struct {
		query      string
		hold, keep float64
		labels     map[string]string
	}{
		{query: "private invalid query"},
		{query: strings.Repeat("x", api.ObsRuleCoverageExpressionMaxBytes+1)},
		{query: "up", hold: -1}, {query: "up", keep: -1},
		{query: "up", hold: math.NaN()}, {query: "up", keep: math.Inf(1)},
		{query: "up", labels: map[string]string{"bad-label": "private value"}},
		{query: "up", labels: map[string]string{"a": strings.Repeat("v", api.ObsRuleCoverageLabelValueMaxBytes+1)}},
	} {
		if _, err := Normalize(tc.query, tc.hold, tc.keep, tc.labels); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("invalid definition accepted or leaked parser error: %v", err)
		}
	}
}

func TestCatalogDriftAndIsolation(t *testing.T) {
	generated, err := Generate(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, catalogJSON) {
		t.Fatal("expected rule catalog drift; run make obs-rule-catalog-generate")
	}
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	core, full := catalog.Sets[0], catalog.Sets[1]
	if core.ID != CoreSetID || full.ID != FullSetID || len(core.Files) != 1 || core.Files[0] != "faas.rules.yml" || len(full.Files) != 5 || len(core.Rules) < 200 || len(full.Rules) <= len(core.Rules) {
		t.Fatalf("unexpected catalog membership: core=%d full=%d", len(core.Rules), len(full.Rules))
	}
	fullIDs := map[string]bool{}
	for _, r := range full.Rules {
		fullIDs[r.ID] = true
	}
	for _, name := range []string{"FaasDataRepairStalled", "FaasDataRepairNodeUnavailable", "FaasDataRepairOutcomeUnknown", "FaasDataRepairHealthStale"} {
		id, ok := Identity("data_repair.rules.yml", "data-repair-health", name)
		if !ok || !fullIDs[id] {
			t.Errorf("full expectation set omits shipped repair alert %s", name)
		}
	}
	for _, r := range core.Rules {
		if !fullIDs[r.ID] || r.FileName != core.Files[0] {
			t.Fatal("core is not a subset of full")
		}
	}
	catalog.Sets[0].Files[0] = "mutated"
	catalog.Sets[0].Rules[0].RuleName = "Mutated"
	fresh, err := Load()
	if err != nil || fresh.Sets[0].Files[0] == "mutated" || fresh.Sets[0].Rules[0].RuleName == "Mutated" {
		t.Fatal("catalog load exposed shared mutable data")
	}
}

func TestCatalogRejectsCorruption(t *testing.T) {
	for _, change := range []func(*Catalog){
		func(c *Catalog) { c.SchemaVersion++ },
		func(c *Catalog) { c.Sets[1].ID = c.Sets[0].ID },
		func(c *Catalog) { c.Sets[0].Revision = strings.Repeat("0", 64) },
		func(c *Catalog) { c.Sets[0].Rules[0].Definition.ForSeconds++ },
		func(c *Catalog) {
			c.Sets[0].Rules[0].ID = c.Sets[0].Rules[1].ID
			c.Sets[0].Revision = revision(c.Sets[0])
		},
	} {
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		change(&c)
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decode(b); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatal("corrupt catalog accepted")
		}
	}
	for _, b := range [][]byte{[]byte(`{}`), append(bytes.Clone(catalogJSON), []byte(` {}`)...), []byte(strings.Replace(string(catalogJSON), `"schema_version": 1`, `"unexpected": true, "schema_version": 1`, 1))} {
		if _, err := decode(b); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatal("invalid catalog encoding accepted")
		}
	}
}

func TestGeneratorRejectsUnsupportedSource(t *testing.T) {
	root := t.TempDir()
	valid := "groups:\n- name: example\n  rules:\n  - alert: ExampleDown\n    expr: up == 0\n"
	for _, rel := range sourceFiles {
		file := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(valid), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Generate(root); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, "    expr:", "    record: example\n    expr:", 1),
		strings.Replace(valid, "  rules:", "  labels: {team: ops}\n  rules:", 1),
		valid + "- name: example\n  rules: []\n",
		strings.Replace(valid, "up == 0", "private invalid query", 1),
		strings.Replace(valid, "    expr:", "    for: -1m\n    expr:", 1),
	} {
		if err := os.WriteFile(filepath.Join(root, sourceFiles[0]), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(root); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid or unsafe source error: %v", err)
		}
	}
}
