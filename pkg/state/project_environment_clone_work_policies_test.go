// adr: 581
package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestCloneWorkPolicyCaptureCoversEveryPolicyAndBindingField(t *testing.T) {
	for _, contract := range []struct {
		source any
		fields map[string]string
	}{
		{workpolicy.Policy{}, map[string]string{"Name": "Name", "MaxRunningPerKey": "MaxRunningPerKey", "MaxRunningPerFairnessKey": "MaxRunningPerFairnessKey", "PendingUpdates": "PendingUpdates", "Debounce": "DebounceMS", "ExpiresAfter": "ExpiresAfterMS"}},
		{EventWorkBinding{}, map[string]string{"SubscriptionID": "SubscriptionID", "AppID": "catalogue_app_id", "PolicyName": "PolicyName", "KeySelector": "KeySelector", "FairnessSelector": "FairnessSelector", "Action": "Action"}},
		{TriggerWorkBinding{}, map[string]string{"TriggerID": "TriggerID", "AppID": "catalogue_app_id", "PolicyName": "PolicyName", "KeySelector": "KeySelector", "FairnessSelector": "FairnessSelector"}},
	} {
		source := reflect.TypeOf(contract.source)
		if source.NumField() != len(contract.fields) {
			t.Fatalf("%s gained configuration without a capture decision", source.Name())
		}
		for i := 0; i < source.NumField(); i++ {
			if contract.fields[source.Field(i).Name] == "" {
				t.Fatalf("%s.%s has no frozen capture mapping", source.Name(), source.Field(i).Name)
			}
		}
	}
}

func cloneWorkPolicyFixture() ProjectEnvironmentCloneWorkPolicyDefinitions {
	return ProjectEnvironmentCloneWorkPolicyDefinitions{Version: 1, AppID: uuid.NewString(), SourceScope: "production",
		Policies: []ProjectEnvironmentCloneWorkPolicy{
			{Name: "orders", Revision: 2, MaxRunningPerKey: 1, PendingUpdates: "keep_latest", DebounceMS: 100, ExpiresAfterMS: 1000, MaxRunningPerFairnessKey: 2},
			{Name: "invoices", Revision: 1, MaxRunningPerKey: 1, PendingUpdates: "all"},
		},
		EventBindings:   []ProjectEnvironmentCloneEventWorkPolicyBinding{{SubscriptionID: uuid.NewString(), PolicyName: "orders", KeySelector: "data.order_id", FairnessSelector: "data.tenant_id", Action: EventWorkInvoke}},
		TriggerBindings: []ProjectEnvironmentCloneTriggerWorkPolicyBinding{{TriggerID: uuid.NewString(), PolicyName: "invoices", KeySelector: "data.invoice_id"}},
	}
}

func TestCloneWorkPolicyCatalogueCanonicalizesAndRejectsBrokenGraph(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*ProjectEnvironmentCloneWorkPolicyDefinitions)
	}{
		{"version", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.Version++ }},
		{"scope", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.SourceScope = "invalid.scope" }},
		{"revision", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.Policies[0].Revision = 0 }},
		{"duration_overflow", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.Policies[0].ExpiresAfterMS = 1 << 62 }},
		{"empty_pending_mode", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.Policies[0].PendingUpdates = "" }},
		{"duplicate_policy", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.Policies = append(d.Policies, d.Policies[0]) }},
		{"unknown_event_policy", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.EventBindings[0].PolicyName = "absent" }},
		{"unknown_trigger_policy", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.TriggerBindings[0].PolicyName = "absent" }},
		{"invalid_event_identity", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) {
			d.EventBindings[0].SubscriptionID = uuid.Nil.String()
		}},
		{"invalid_trigger_selector", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.TriggerBindings[0].KeySelector = "data.*" }},
		{"invalid_fairness_selector", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.EventBindings[0].FairnessSelector = "data.*" }},
		{"invalid_action", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) { d.EventBindings[0].Action = "future-action" }},
		{"duplicate_event", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) {
			d.EventBindings = append(d.EventBindings, d.EventBindings[0])
		}},
		{"duplicate_trigger", func(d *ProjectEnvironmentCloneWorkPolicyDefinitions) {
			d.TriggerBindings = append(d.TriggerBindings, d.TriggerBindings[0])
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			definitions := cloneWorkPolicyFixture()
			change.edit(&definitions)
			if _, err := normalizeCloneWorkPolicyDefinitions(definitions); !errors.Is(err, ErrConflict) {
				t.Fatalf("broken policy graph was accepted: %v", err)
			}
		})
	}
	definitions := cloneWorkPolicyFixture()
	before, _ := json.Marshal(definitions)
	canonical, err := normalizeCloneWorkPolicyDefinitions(definitions)
	if err != nil || canonical.Policies[0].Name != "invoices" {
		t.Fatalf("canonical catalogue: %+v %v", canonical, err)
	}
	after, _ := json.Marshal(definitions)
	if string(before) != string(after) {
		t.Fatal("normalization mutated its input")
	}
	hash, err := cloneWorkPolicyDefinitionsHash(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalHash, err := cloneWorkPolicyDefinitionsHash(canonical); err != nil || canonicalHash != hash {
		t.Fatalf("input order changed source revision: %v", err)
	}
	canonical.Policies[0].Revision++
	if reflect.DeepEqual(canonical.Policies, definitions.Policies) || definitions.Policies[1].Revision != 1 {
		t.Fatal("normalized policy values alias input")
	}
}

func TestCloneWorkPoliciesRequireIsolationEvenForEqualOrEmptyCatalogue(t *testing.T) {
	for _, empty := range []bool{false, true} {
		work := cloneWorkPolicyFixture()
		if empty {
			work.Policies, work.EventBindings, work.TriggerBindings = nil, nil, nil
		}
		source := projectCloneScopedPolicies{Work: &work}
		record := projectCloneWorkloadRecord{ProjectEnvironmentCloneWorkload: ProjectEnvironmentCloneWorkload{WorkloadSlug: "api"}, snapshot: projectCloneWorkloadSnapshot{Policies: &source}}
		record.SourcePoliciesHash, _ = cloneScopedPoliciesHash(source)
		for _, actual := range []projectCloneScopedPolicies{{}, source} {
			if err := validateCloneScopedPolicyPublication(record, actual, true); !errors.Is(err, ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable) {
				t.Fatalf("matching/empty definitions proved isolated runtime admission: %v", err)
			}
		}
	}
	legacy := projectCloneScopedPolicies{}
	raw, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(raw), "work") {
		t.Fatalf("legacy policy bytes changed: %s %v", raw, err)
	}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Policies: &legacy}}
	record.SourcePoliciesHash, _ = cloneScopedPoliciesHash(legacy)
	if err := validateCloneScopedPolicyPublication(record, legacy, true); err != nil {
		t.Fatalf("legacy policy publication contract changed: %v", err)
	}
}
