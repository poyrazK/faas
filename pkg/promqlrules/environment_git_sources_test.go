package promqlrules_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnvironmentGitSourceAlertsStayInternal(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert  string            `yaml:"alert"`
				Labels map[string]string `yaml:"labels"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, group := range doc.Groups {
		if group.Name != "faas_environment_git_sources" {
			continue
		}
		for _, rule := range group.Rules {
			seen++
			if rule.Labels["public_status"] != "internal" || rule.Labels["component"] != "apid" || rule.Labels["family"] != "environment_git_sources" {
				t.Errorf("%s could misclassify discovery health as a public serving incident: %v", rule.Alert, rule.Labels)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("expected three source-health alerts, got %d", seen)
	}
}
