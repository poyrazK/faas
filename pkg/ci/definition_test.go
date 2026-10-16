package ci_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func definition() ci.Definition {
	return ci.Definition{Version: 1, Repository: "acme/api", Jobs: map[string]ci.Job{
		"test":  {Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), Steps: []ci.Step{{Name: "test", Run: "npm ci && npm test"}}, Artifacts: []string{"reports/**"}},
		"build": {Image: "registry.example/ci@sha256:" + strings.Repeat("b", 64), Needs: []string{"test"}, Steps: []ci.Step{{Name: "build", Run: "npm run build"}}},
	}}
}

func TestCompilePinsAndOrdersIndependentOfMapOrder(t *testing.T) {
	def := definition()
	before, _ := json.Marshal(def)
	plan, canonical, err := ci.Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Jobs) != 2 || plan.Jobs[0].Name != "test" || plan.Jobs[1].Name != "build" || plan.Jobs[0].TimeoutSeconds != api.CIDefaultTimeoutSeconds {
		t.Fatalf("invalid plan: %+v", plan)
	}
	after, _ := json.Marshal(def)
	if string(before) != string(after) {
		t.Fatal("compile mutated its input")
	}
	parsed, err := ci.Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := ci.Compile(parsed)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("unstable canonical plan: %v", err)
	}
}

func TestDefinitionRejectsUnsupportedOrAmbiguousWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ci.Definition)
	}{
		{"version", func(d *ci.Definition) { d.Version = 2 }},
		{"repository", func(d *ci.Definition) { d.Repository = "https://user:password@github.com/acme/api" }},
		{"unpinned-image", func(d *ci.Definition) { j := d.Jobs["test"]; j.Image = "node:24"; d.Jobs["test"] = j }},
		{"missing-dependency", func(d *ci.Definition) { j := d.Jobs["test"]; j.Needs = []string{"missing"}; d.Jobs["test"] = j }},
		{"cycle", func(d *ci.Definition) { j := d.Jobs["test"]; j.Needs = []string{"build"}; d.Jobs["test"] = j }},
		{"timeout", func(d *ci.Definition) {
			j := d.Jobs["test"]
			j.TimeoutSeconds = api.CIMaxTimeoutSeconds + 1
			d.Jobs["test"] = j
		}},
		{"artifact-traversal", func(d *ci.Definition) {
			j := d.Jobs["test"]
			j.Artifacts = []string{"reports/../secret"}
			d.Jobs["test"] = j
		}},
		{"artifact-absolute", func(d *ci.Definition) { j := d.Jobs["test"]; j.Artifacts = []string{"/etc/passwd"}; d.Jobs["test"] = j }},
		{"duplicate-step", func(d *ci.Definition) { j := d.Jobs["test"]; j.Steps = append(j.Steps, j.Steps[0]); d.Jobs["test"] = j }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := definition()
			tc.mutate(&def)
			if _, _, err := ci.Compile(def); err == nil {
				t.Fatal("invalid work accepted")
			}
		})
	}
	for _, input := range []string{
		"version: 1\nversion: 2\n", "version: &v 1\nrepository: *v\n", "version: 1\n---\nversion: 1\n",
		"version: 1\nrepository: acme/api\njobs: {}\nsecrets: {}\n",
		`{"version":1,"version":1,"repository":"acme/api","jobs":{}}`,
		strings.Repeat(" ", api.CIDefinitionMaxBytes+1),
	} {
		if _, err := ci.Parse([]byte(input)); err == nil {
			t.Fatalf("ambiguous input accepted: %.80q", input)
		}
	}
}

func TestInternalConfigRequiresExactFlagAndAllowlist(t *testing.T) {
	account := "a2b5b77c-c9cb-4f75-a7f4-c14f7d54be25"
	for _, tc := range []struct {
		flag, accounts string
		want           bool
	}{
		{"", account, false}, {"true", account, false}, {"1", "", false}, {"1", account, true}, {"1", account + ",invalid", false},
	} {
		cfg := ci.ConfigFromEnv(func(key string) string {
			if key == ci.InternalEnv {
				return tc.flag
			}
			if key == ci.AccountsEnv {
				return tc.accounts
			}
			return ""
		})
		if cfg.Allows(account) != tc.want || cfg.Allows(strings.ReplaceAll(account, "-", "")) != tc.want || cfg.Allows("other") {
			t.Fatalf("gate mismatch for %q/%q", tc.flag, tc.accounts)
		}
	}
	if (ci.Config{}).Allows(account) {
		t.Fatal("zero config enabled CI")
	}
}
