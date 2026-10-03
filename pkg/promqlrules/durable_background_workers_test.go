package promqlrules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurableBackgroundWorkerAlertsCoverClaimersAndRollup(t *testing.T) {
	rulesPath := filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read Prometheus rules: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"alert: FaasManagedRealtimeDrainClaimFailed",
		`apid_ops_total{op="managed_realtime_drain_claim",code="err"}`,
		"alert: FaasJobImageMaterializationClaimFailed",
		`imaged_ops_total{op="job_materialization_claim",code="err"}`,
		"alert: FaasUsageDailyRollupFailed",
		`meterd_ops_total{op="usage_daily_rollup",code="err"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("faas.rules.yml missing %q", want)
		}
	}
}

func TestAppLifecycleWebhookTransitionAlertsUseFleetHealth(t *testing.T) {
	rulesPath := filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read Prometheus rules: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"alert: FaasAppLifecycleTransitionStalled",
		"schedd_app_lifecycle_transition_oldest_pending_seconds > 300",
		"alert: FaasAppLifecycleTransitionHealthPollFailed",
		"schedd_app_lifecycle_transition_health_poll_success == 0",
		"docs/runbooks/FaasWebhookDelivery.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("faas.rules.yml missing %q", want)
		}
	}
}

func TestWebhookEventOutboxBacklogAlertsUseFleetHealth(t *testing.T) {
	rulesPath := filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read Prometheus rules: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"alert: FaasWebhookEventOutboxBacklog",
		"schedd_webhook_event_outbox_oldest_pending_seconds > 300",
		"alert: FaasWebhookEventOutboxHealthPollFailed",
		"schedd_webhook_event_outbox_health_poll_success == 0",
		"docs/runbooks/FaasWebhookDelivery.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("faas.rules.yml missing %q", want)
		}
	}
}
