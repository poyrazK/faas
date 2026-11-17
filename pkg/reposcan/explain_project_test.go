package reposcan_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

// ADR-20261107180000 `gregale config explain`: each project setting reports
// its effective value and the layer it came from.
func explainFixture(projectBlock string) fstest.MapFS {
	return fstest.MapFS{
		"compose.yaml": {Data: []byte(`services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    depends_on: [api]
    x-gregale-service-policy: project
  api:
    image: nginx:1.27
    ports: ["8081:80"]
  worker:
    image: nginx:1.27
`)},
		"gregale.yaml": {Data: []byte(projectBlock)},
	}
}

func winner(t *testing.T, got reposcan.SettingExplanation) reposcan.SettingLayer {
	t.Helper()
	var wins []reposcan.SettingLayer
	for _, layer := range got.Layers {
		if layer.Wins {
			wins = append(wins, layer)
		}
	}
	if len(wins) != 1 {
		t.Fatalf("%s: winning layers = %+v, want exactly one", got.Key, wins)
	}
	return wins[0]
}

func TestExplainProjectSetting(t *testing.T) {
	block := `project:
  service_policy: declared
  services:
    web:
      service_policy: account
      depends_on: [worker]
    api:
      visibility: internal
`
	tests := []struct {
		name, block, key, value, winSource, winField string
	}{
		{"compose policy beats the block", block, "services.web.service_policy", "project", "compose.yaml: web", "x-gregale-service-policy"},
		{"service block policy beats project default", block, "services.api.service_policy", "declared", "gregale.yaml `project:`", "service_policy"},
		{"project policy", block, "service_policy", "declared", "gregale.yaml `project:`", "service_policy"},
		{"default policy", "project:\n  name: shop\n", "services.api.service_policy", "project", "default", ""},
		{"block visibility beats ports", block, "services.api.visibility", "internal", "gregale.yaml `project:`", "services.api.visibility"},
		{"detected visibility", block, "services.web.visibility", "public", "compose.yaml: web", ""},
		{"service names match case-insensitively", block, "services.API.visibility", "internal", "gregale.yaml `project:`", "services.API.visibility"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reposcan.ExplainProjectSetting(explainFixture(tt.block), tt.key)
			if err != nil {
				t.Fatal(err)
			}
			if got.Value != tt.value {
				t.Fatalf("value = %q, want %q (layers %+v)", got.Value, tt.value, got.Layers)
			}
			win := winner(t, got)
			if !strings.HasPrefix(win.Source, tt.winSource) || win.Field != tt.winField {
				t.Fatalf("winner = %+v, want %s %s", win, tt.winSource, tt.winField)
			}
		})
	}
}

func TestExplainProjectSettingMergesDependsOn(t *testing.T) {
	got, err := reposcan.ExplainProjectSetting(explainFixture("project:\n  services:\n    web:\n      depends_on: [worker]\n"), "services.web.depends_on")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "api, worker" {
		t.Fatalf("value = %q, want the Compose list merged with the block's", got.Value)
	}
	for _, layer := range got.Layers {
		if !layer.Set || !layer.Wins {
			t.Fatalf("layer %+v: both lists contribute", layer)
		}
	}
}

func TestExplainProjectSettingLegacyFile(t *testing.T) {
	fsys := explainFixture("")
	delete(fsys, "gregale.yaml")
	fsys[".gregale/project.yaml"] = &fstest.MapFile{Data: []byte("services:\n  api:\n    visibility: internal\n")}
	got, err := reposcan.ExplainProjectSetting(fsys, "services.api.visibility")
	if err != nil {
		t.Fatal(err)
	}
	if win := winner(t, got); got.Value != "internal" || win.Source != ".gregale/project.yaml" {
		t.Fatalf("got %q from %+v, want internal from the legacy file", got.Value, win)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "gregale project import") {
		t.Fatalf("warnings = %v, want the deprecation hint", got.Warnings)
	}
}

func TestExplainProjectSettingRejects(t *testing.T) {
	for _, key := range []string{"services.missing.visibility", "services.api.memory", "visibility", "services..visibility"} {
		if _, err := reposcan.ExplainProjectSetting(explainFixture("project:\n  name: shop\n"), key); err == nil {
			t.Errorf("%q: want an error", key)
		}
	}
}
