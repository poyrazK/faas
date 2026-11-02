package gregalemanifest

import (
	"maps"
	"strings"
	"testing"
)

// gregale.yaml and gregale.toml declare an app's labels
// (ADR-20261031100025 §7); Validate refuses what the label API refuses.
func TestManifestLabels(t *testing.T) {
	yamlManifest, err := ParseBytes([]byte("labels:\n  team: billing\n  tier: web\n  canary: \"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"team": "billing", "tier": "web", "canary": ""}; !maps.Equal(yamlManifest.Labels, want) {
		t.Fatalf("yaml labels = %v, want %v", yamlManifest.Labels, want)
	}
	if err := yamlManifest.Validate(); err != nil {
		t.Fatalf("valid labels refused: %v", err)
	}
	tomlManifest, err := ParseTOMLBytes([]byte("[labels]\nteam = \"billing\"\n\"example.com/owner\" = \"ops\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"team": "billing", "example.com/owner": "ops"}; !maps.Equal(tomlManifest.Labels, want) {
		t.Fatalf("toml labels = %v, want %v", tomlManifest.Labels, want)
	}

	refused := map[string]string{
		"labels:\n  bad key: v\n":            "labels: \"bad key\"",
		"labels:\n  team: -billing\n":        "labels: \"team\"",
		"labels:\n  gregale.dev/owner: me\n": "belong to Gregale",
	}
	for doc, want := range refused {
		m, err := ParseBytes([]byte(doc))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: Validate = %v, want %q", doc, err, want)
		}
	}
}
