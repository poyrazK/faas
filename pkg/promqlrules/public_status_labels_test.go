package promqlrules_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSnapshotFleetCapacityAlertsStayInternal(t *testing.T) {
	rulesPath := filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read Prometheus rules: %v", err)
	}

	var doc struct {
		Groups []struct {
			Rules []struct {
				Alert  string            `yaml:"alert"`
				Labels map[string]string `yaml:"labels"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse Prometheus rules: %v", err)
	}

	want := map[string]bool{
		"FaasSnapshotFleetAvgHighPage": false,
		"FaasSnapshotFleetAvgHighWarn": false,
	}
	for _, group := range doc.Groups {
		for _, rule := range group.Rules {
			if _, ok := want[rule.Alert]; !ok {
				continue
			}
			want[rule.Alert] = true
			if got := rule.Labels["public_status"]; got != "internal" {
				t.Errorf("%s public_status=%q, want internal", rule.Alert, got)
			}
		}
	}
	for alert, found := range want {
		if !found {
			t.Errorf("Prometheus rule %s not found", alert)
		}
	}
}
