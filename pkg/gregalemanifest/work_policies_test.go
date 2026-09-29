package gregalemanifest

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestManifestWorkPoliciesYAMLAndTOML(t *testing.T) {
	yamlManifest, err := ParseBytes([]byte(`work_policies:
  - name: document-index
    max_running_per_key: 1
    max_running_per_fairness_key: 2
    pending_updates: keep_latest
    debounce_ms: 3000
    expires_after_ms: 600000
event_triggers:
  - source: documents
    type: document.edited
    work_policy: document-index
    work_key: data.document_id
    work_fairness_key: data.tenant_id
`))
	if err != nil || yamlManifest.ValidateForPlan(api.PlanPro) != nil {
		t.Fatalf("YAML = %+v, %v", yamlManifest, err)
	}
	if len(yamlManifest.WorkPolicies) != 1 || yamlManifest.WorkPolicies[0].ToPolicy().PendingUpdates != workpolicy.PendingKeepLatest ||
		yamlManifest.WorkPolicies[0].ToPolicy().MaxRunningPerFairnessKey != 2 {
		t.Fatalf("YAML policies = %+v", yamlManifest.WorkPolicies)
	}
	tomlManifest, err := ParseTOMLBytes([]byte(`[[work_policies]]
name = "document-index"
max_running_per_key = 1
max_running_per_fairness_key = 2
pending_updates = "keep_latest"
debounce_ms = 3000
expires_after_ms = 600000

[[triggers.event]]
source = "documents"
type = "document.edited"
work_policy = "document-index"
work_key = "data.document_id"
work_fairness_key = "data.tenant_id"
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := tomlManifest.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if len(tomlManifest.WorkPolicies) != 1 || tomlManifest.WorkPolicies[0].Name != "document-index" {
		t.Fatalf("TOML policies = %+v", tomlManifest.WorkPolicies)
	}
}

func TestManifestWorkPoliciesRejectInvalidAndDuplicate(t *testing.T) {
	manifest := &Manifest{WorkPolicies: []WorkPolicy{{Name: "index", MaxRunningPerKey: 1}, {Name: "index", MaxRunningPerKey: 1}}}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate = %v", err)
	}
	manifest.WorkPolicies[1].Name = "other"
	manifest.WorkPolicies[1].MaxRunningPerKey = 2
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "max_running_per_key") {
		t.Fatalf("unsupported policy = %v", err)
	}
}

func TestBrokerTriggerWorkBindingManifest(t *testing.T) {
	manifest, err := ParseBytes([]byte(`work_policies:
  - app: orders
    name: order-work
    max_running_per_key: 1
triggers:
  - kind: kafka
    app: orders
    slug: order-events
    config:
      brokers: [localhost:9092]
      topic: orders
      group: workers
    work_policy: order-work
    work_key: order_id
    work_fairness_key: tenant_id
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatal(err)
	}
	manifest.Triggers[0].WorkKey = "order_id.*"
	if err := manifest.ValidateForPlan(api.PlanPro); err == nil || !strings.Contains(err.Error(), "work_key") {
		t.Fatalf("invalid selector = %v", err)
	}
	manifest.Triggers[0].WorkKey = "order_id"
	manifest.Triggers[0].Kind = TriggerKindCron
	manifest.Triggers[0].Schedule = "* * * * *"
	manifest.Triggers[0].Path = "/"
	if err := manifest.ValidateForPlan(api.PlanPro); err == nil || !strings.Contains(err.Error(), "work policies require") {
		t.Fatalf("queue policy binding = %v", err)
	}
}

func TestEventTriggerCancelPendingRequiresPolicyKey(t *testing.T) {
	trigger := EventTrigger{Source: "orders", Type: "order.completed", WorkAction: "cancel_pending"}
	if err := trigger.Validate(0); err == nil || !strings.Contains(err.Error(), "requires work_policy") {
		t.Fatalf("missing policy/key = %v", err)
	}
	trigger.WorkPolicy, trigger.WorkKey = "reminders", "data.order_id"
	if err := trigger.Validate(0); err != nil {
		t.Fatalf("valid cancellation trigger: %v", err)
	}
}
