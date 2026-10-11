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

func TestManifestExclusiveOperationsYAMLAndTOML(t *testing.T) {
	yamlManifest, err := ParseBytes([]byte(`exclusive_operations:
  policies:
    - name: crm-sync
      scope: platform_tenant
      member_apps: [crm-api]
      contention: queue
      lease_seconds: 30
      max_attempt_seconds: 600
      max_attempts: 5
    - name: nightly-import
      scope: account
      member_job_ids: [9a9bbd32-4996-4e46-8670-e2721ad18451]
      contention: queue
      lease_seconds: 30
      max_attempt_seconds: 600
  bindings:
    - source: cron
      app: crm-api
      schedule: "*/15 * * * *"
      path: /sync
      policy: crm-sync
      key: customer-sync
      platform_tenant_id: 5e57edce-03ad-4a1e-8a92-e2bd5e0a4253
    - source: job_schedule
      job: nightly-import
      policy: nightly-import
      key: nightly-import
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := yamlManifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(yamlManifest.ExclusiveOperations.Policies); got != 2 {
		t.Fatalf("YAML policies=%d, want 2", got)
	}
	if got := yamlManifest.ExclusiveOperations.Policies[1].MemberJobIDs; len(got) != 1 || got[0] != "9a9bbd32-4996-4e46-8670-e2721ad18451" {
		t.Fatalf("YAML Job members=%v", got)
	}
	key, err := yamlManifest.ExclusiveOperations.Bindings[0].KeyJSON()
	if err != nil || string(key) != `"customer-sync"` {
		t.Fatalf("YAML key=%s, err=%v", key, err)
	}
	if got := yamlManifest.ExclusiveOperations.Bindings[1].Job; got != "nightly-import" {
		t.Fatalf("YAML Job schedule selector=%q", got)
	}

	tomlManifest, err := ParseTOMLBytes([]byte(`[exclusive_operations]

[[exclusive_operations.policies]]
name = "crm-sync"
scope = "account"
member_apps = ["crm-api"]
contention = "join_existing"
lease_seconds = 30
max_attempt_seconds = 600

[[exclusive_operations.policies]]
name = "nightly-import"
scope = "account"
member_job_ids = ["9a9bbd32-4996-4e46-8670-e2721ad18451"]
contention = "queue"
lease_seconds = 30
max_attempt_seconds = 600

[[exclusive_operations.bindings]]
source = "broker"
app = "crm-api"
kind = "kafka"
name = "crm-events"
policy = "crm-sync"
key = 42
equivalence_key = "same-customer-sync"

[[exclusive_operations.bindings]]
source = "job_schedule"
job = "nightly-import"
policy = "nightly-import"
key = "nightly-import"
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := tomlManifest.Validate(); err != nil {
		t.Fatal(err)
	}
	key, err = tomlManifest.ExclusiveOperations.Bindings[0].KeyJSON()
	if err != nil || string(key) != "42" {
		t.Fatalf("TOML key=%s, err=%v", key, err)
	}
	if got := tomlManifest.ExclusiveOperations.Bindings[1].Job; got != "nightly-import" {
		t.Fatalf("TOML Job schedule selector=%q", got)
	}
}

func TestManifestExclusiveOperationsRejectUnsafeOrAmbiguousBindings(t *testing.T) {
	base := ExclusiveOperationsConfig{
		Policies: []ExclusiveOperationPolicy{{Name: "crm-sync", Scope: "platform_tenant", MemberApps: []string{"crm-api"}, Contention: "queue", LeaseSeconds: 30, MaxAttemptSeconds: 600}},
		Bindings: []ExclusiveOperationBinding{{Source: "cron", App: "crm-api", Schedule: "*/15 * * * *", Path: "/sync", Policy: "crm-sync", Key: "sync"}},
	}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "platform_tenant_id") {
		t.Fatalf("missing tenant scope = %v", err)
	}
	base.Bindings[0].PlatformTenantID = "5e57edce-03ad-4a1e-8a92-e2bd5e0a4253"
	base.Bindings[0].Key = map[string]any{"tenant": "untrusted"}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "JSON string, number, or boolean") {
		t.Fatalf("object business key = %v", err)
	}
	base.Bindings[0].Key = "sync"
	base.Bindings[0].Source = "broker"
	base.Bindings[0].Schedule = ""
	base.Bindings[0].Path = ""
	base.Bindings[0].Kind = "unknown"
	base.Bindings[0].Name = "events"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "supported broker trigger kind") {
		t.Fatalf("unknown broker kind = %v", err)
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

func TestOrderedEventTriggerRequiresSerialPolicy(t *testing.T) {
	unsafe := &Manifest{
		WorkPolicies: []WorkPolicy{{Name: "event-order", MaxRunningPerKey: 1,
			PendingUpdates: string(workpolicy.PendingKeepLatest)}},
		EventTriggers: []EventTrigger{{Source: "orders", Type: "order.changed",
			WorkPolicy: "event-order", WorkKey: "data.order_id", Ordered: true}},
	}
	if err := unsafe.ValidateForPlan(api.PlanPro); err == nil || !strings.Contains(err.Error(), "pending_updates=all") {
		t.Fatalf("unsafe ordered policy = %v", err)
	}
	unsafe.WorkPolicies[0] = WorkPolicy{Name: "event-order", MaxRunningPerKey: 2, PendingUpdates: string(workpolicy.PendingAll)}
	if err := unsafe.ValidateForPlan(api.PlanPro); err == nil || !strings.Contains(err.Error(), "max_running_per_key") {
		t.Fatalf("parallel ordered policy = %v", err)
	}
	unsafe.WorkPolicies[0] = WorkPolicy{Name: "event-order", MaxRunningPerKey: 1, PendingUpdates: string(workpolicy.PendingAll)}
	if err := unsafe.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatalf("serial ordered policy: %v", err)
	}
}
