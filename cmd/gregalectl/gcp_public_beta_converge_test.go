package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGCPInfrastructureIAMAlertUsesSupportedResourceTypes(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "gcp", "infrastructure-iam-change-alert.json"))
	if err != nil {
		t.Fatalf("read infrastructure IAM alert: %v", err)
	}
	var policy struct {
		Conditions []struct {
			ConditionThreshold struct {
				Filter string `json:"filter"`
			} `json:"conditionThreshold"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatalf("parse infrastructure IAM alert: %v", err)
	}

	filters := make(map[string]bool, len(policy.Conditions))
	for _, condition := range policy.Conditions {
		filters[condition.ConditionThreshold.Filter] = true
	}
	metric := `metric.type="logging.googleapis.com/user/gregale_infrastructure_iam_change"`
	for _, resourceType := range []string{"global", "gcs_bucket"} {
		want := `resource.type="` + resourceType + `" AND ` + metric
		if !filters[want] {
			t.Errorf("IAM alert is missing supported resource filter %q", want)
		}
	}
	for filter := range filters {
		if strings.Contains(filter, `resource.type="project"`) {
			t.Errorf("IAM alert uses unsupported log-metric resource type: %q", filter)
		}
	}
}

func TestGCPGuardConvergesAfterPartialApply(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ops", "gcp_public_beta_converge.sh"))
	if err != nil {
		t.Fatalf("read GCP converge script: %v", err)
	}
	script := string(raw)
	for _, required := range []string{
		`if [[ "$boot_auto_delete" != "False" ]]`,
		`for _ in $(seq 1 12)`,
		`for attempt in $(seq 1 30)`,
		`create_monitoring_policy "$root/deploy/gcp/${metric}-alert.json" "$channel"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("GCP guard is missing partial-apply convergence contract %q", required)
		}
	}
	if strings.Contains(script, `displayName="Gregale operator email" AND enabled=true`) {
		t.Fatal("notification lookup still uses the unsupported enabled filter")
	}
}
