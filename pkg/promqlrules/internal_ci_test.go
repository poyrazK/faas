package promqlrules_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCIAlertsAndDashboardPreserveInternalUnknownEvidence(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "deploy/ansible/roles/prometheus/files/faas.rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Record string            `yaml:"record"`
				Alert  string            `yaml:"alert"`
				Labels map[string]string `yaml:"labels"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	records := make(map[string]bool)
	alerts := 0
	for _, group := range doc.Groups {
		if group.Name != "faas_internal_ci" {
			continue
		}
		for _, rule := range group.Rules {
			if rule.Record != "" {
				records[rule.Record] = true
				continue
			}
			alerts++
			if rule.Labels["public_status"] != "internal" || rule.Labels["family"] != "internal_ci" || rule.Labels["severity"] != "warn" || rule.Labels["component"] != "platform" {
				t.Fatalf("CI operational alert escaped its internal warning scope: %s", rule.Alert)
			}
		}
	}
	if alerts != 5 || len(records) != 15 {
		t.Fatalf("CI operations contract missing: %d alerts, %d records", alerts, len(records))
	}
	canonical, err := os.ReadFile(filepath.Join(root, "deploy/grafana/internal-ci.json"))
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := os.ReadFile(filepath.Join(root, "deploy/ansible/roles/grafana/files/internal-ci.json"))
	if err != nil || !bytes.Equal(canonical, mirror) {
		t.Fatal("dashboard provisioning mirror is out of sync")
	}
	var dashboard struct {
		UID    string `json:"uid"`
		Panels []struct {
			Title   string `json:"title"`
			Type    string `json:"type"`
			Targets []struct {
				Expr    string `json:"expr"`
				Instant bool   `json:"instant"`
			} `json:"targets"`
			FieldConfig struct {
				Defaults struct {
					NoValue  string `json:"noValue"`
					Mappings []struct {
						Type    string          `json:"type"`
						Options json.RawMessage `json:"options"`
					} `json:"mappings"`
				} `json:"defaults"`
			} `json:"fieldConfig"`
			Options struct {
				ReduceOptions struct {
					Calcs []string `json:"calcs"`
				} `json:"reduceOptions"`
			} `json:"options"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(canonical, &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard.UID != "faas-internal-ci" || len(dashboard.Panels) < 8 {
		t.Fatal("internal operator dashboard missing")
	}
	for _, panel := range dashboard.Panels {
		if panel.FieldConfig.Defaults.NoValue != "Unknown" {
			t.Fatalf("%s can render missing evidence as healthy", panel.Title)
		}
		for _, target := range panel.Targets {
			expr := strings.TrimPrefix(target.Expr, "100 * ")
			metric := strings.SplitN(expr, "{", 2)[0]
			if !records[metric] {
				t.Fatalf("%s bypasses deduplicated recording rules: %s", panel.Title, target.Expr)
			}
			if panel.Type == "stat" || panel.Type == "table" {
				if !target.Instant {
					t.Fatalf("%s can keep an old status after evidence disappears", panel.Title)
				}
			}
		}
		for _, calc := range panel.Options.ReduceOptions.Calcs {
			if calc == "lastNotNull" {
				t.Fatalf("%s silently reuses old healthy evidence", panel.Title)
			}
		}
		unknownCode := ""
		if panel.Title == "Health read status" {
			unknownCode = "3"
		}
		if panel.Title == "Worker availability" {
			unknownCode = "7"
		}
		mappedUnknown, specialUnknown := false, false
		for _, mapping := range panel.FieldConfig.Defaults.Mappings {
			if mapping.Type == "value" {
				var options map[string]struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(mapping.Options, &options); err != nil {
					t.Fatal(err)
				}
				mappedUnknown = options[unknownCode].Text == "Unknown"
			}
			if mapping.Type == "special" {
				var options struct {
					Match  string `json:"match"`
					Result struct {
						Text  string `json:"text"`
						Color string `json:"color"`
					} `json:"result"`
				}
				if err := json.Unmarshal(mapping.Options, &options); err != nil {
					t.Fatal(err)
				}
				specialUnknown = options.Match == "null" && options.Result.Text == "Unknown" && options.Result.Color != "green"
			}
		}
		if unknownCode != "" && !mappedUnknown {
			t.Fatalf("%s lost the explicit unknown state", panel.Title)
		}
		if !specialUnknown {
			t.Fatalf("%s can color missing evidence as healthy", panel.Title)
		}
	}
	task, err := os.ReadFile(filepath.Join(root, "deploy/ansible/roles/grafana/tasks/main.yml"))
	if err != nil || !bytes.Contains(task, []byte("- internal-ci.json")) {
		t.Fatal("dashboard is not provisioned by the existing Grafana role")
	}
}
