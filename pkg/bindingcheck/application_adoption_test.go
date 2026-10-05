package bindingcheck

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func adoptionFixture(item api.AppBindingInventoryItem, now time.Time) *api.BindingApplicationAdoption {
	at := now.Add(-time.Minute)
	adoption := &api.BindingApplicationAdoption{Source: "application_ack", Complete: true, ObservedAt: now, Targets: []api.BindingApplicationAckTarget{}}
	for _, key := range api.BindingCredentialSecretKeys(item.Type, item.Binding) {
		adoption.Targets = append(adoption.Targets, api.BindingApplicationAckTarget{DeploymentID: "deployment-1", InstanceID: "runtime-1", Key: key, RuntimeState: "running", CurrentVersion: 2,
			ReloadSupport: "enabled", ReloadVersion: 2, Projection: "updated", Signal: "sent", ReloadAt: &at, ApplicationAckVersion: 2, ApplicationAck: "applied", ApplicationAckAt: &at, ProcessGeneration: strings.Repeat("a", 32), ApplicationAckGeneration: strings.Repeat("a", 32)})
	}
	adoption.SecretsExpected, adoption.SecretsObserved = len(adoption.Targets), len(adoption.Targets)
	return adoption
}

func strictAdoptionFixture() (api.AppBindingInventory, Policy, time.Time) {
	inventory, policy, now := fixture()
	policy.RequireApplicationAck = true
	for index := 1; index < len(inventory.Bindings); index++ {
		item := &inventory.Bindings[index]
		item.ApplicationAdoption = adoptionFixture(*item, now)
	}
	return inventory, policy, now
}

func TestApplicationAdoptionCurrentReceiptsAndIndependentVersions(t *testing.T) {
	inventory, policy, now := strictAdoptionFixture()
	original := *inventory.Bindings[1].ApplicationAdoption
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || !report.RequireApplicationAck || report.Bindings[0].ApplicationAdoption.Application.Current != 6 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if !reflect.DeepEqual(original, *inventory.Bindings[1].ApplicationAdoption) {
		t.Fatal("mutated inventory")
	}
	// Application receipts are independent of guest projection and bound to a
	// version rather than the probe age window.
	target := &inventory.Bindings[1].ApplicationAdoption.Targets[0]
	target.Signal = "not_attempted"
	report, err = Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Bindings[1].ApplicationAdoption.Reload.Current == 0 {
		t.Fatalf("startup delivery with independent application ACK: %+v %v", report, err)
	}
	target.ReloadVersion = 1
	old := now.Add(-24 * time.Hour)
	target.ReloadAt, target.ApplicationAckAt = &old, &old
	report, err = Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Bindings[1].ApplicationAdoption.Reload.Stale != 1 {
		t.Fatalf("independent versions: %+v %v", report, err)
	}
}

func TestApplicationAdoptionRejectsMissingFailedStaleAndContradictoryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*api.BindingApplicationAdoption, time.Time)
	}{
		{"startup delivery without application ACK", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) {
			a.Targets[0].Signal = "not_attempted"
			a.Targets[0].ApplicationAckAt = nil
		}},
		{"legacy generation", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ProcessGeneration = "" }},
		{"ACK without generation", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ApplicationAckGeneration = "" }},
		{"previous process ACK", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) {
			a.Targets[0].ProcessGeneration = strings.Repeat("b", 32)
		}},
		{"malformed process", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ProcessGeneration = "private-value" }},
		{"missing report", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ApplicationAckAt = nil }},
		{"old version", "application_ack_stale", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ApplicationAckVersion = 1 }},
		{"application failed", "application_adoption_failed", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ApplicationAck = "failed" }},
		{"projection failed", "application_adoption_failed", func(a *api.BindingApplicationAdoption, _ time.Time) {
			a.Targets[0].Projection, a.Targets[0].Signal = "failed", "not_attempted"
		}},
		{"signal failed", "application_adoption_failed", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].Signal = "failed" }},
		{"reload malformed", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].Projection = "unknown" }},
		{"future ack", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, now time.Time) {
			at := now.Add(time.Nanosecond)
			a.Targets[0].ApplicationAckAt = &at
		}},
		{"future version", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ApplicationAckVersion = 3 }},
		{"disabled", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ReloadSupport = "disabled" }},
		{"legacy support", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].ReloadSupport = "unknown" }},
		{"missing secret", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.SecretsObserved = 0 }},
		{"incomplete read", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Complete = false }},
		{"wrong source", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Source = "instance_started_at" }},
		{"zero time", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.ObservedAt = time.Time{} }},
		{"duplicate", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets = append(a.Targets, a.Targets[0]) }},
		{"bad workload", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].WorkloadName = "../main" }},
		{"wrong secret", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].Key = "OTHER" }},
		{"old deployment", "application_ack_candidate_unobserved", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets[0].DeploymentID = "serving" }},
		{"no residents", "application_ack_candidate_unobserved", func(a *api.BindingApplicationAdoption, _ time.Time) { a.Targets = nil }},
		{"contradictory version", "application_adoption_unknown", func(a *api.BindingApplicationAdoption, _ time.Time) {
			b := a.Targets[0]
			b.InstanceID = "runtime-2"
			b.CurrentVersion = 3
			a.Targets = append(a.Targets, b)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := strictAdoptionFixture()
			tc.change(inventory.Bindings[1].ApplicationAdoption, now)
			policy.AllowUnsupported = true
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), tc.code) {
				t.Fatalf("want %s, report=%+v err=%v", tc.code, report, err)
			}
		})
	}
}

func TestApplicationAdoptionMixedSidecarsAndDefaultPolicy(t *testing.T) {
	inventory, policy, now := strictAdoptionFixture()
	adoption := inventory.Bindings[2].ApplicationAdoption
	sidecar := adoption.Targets[4]
	sidecar.WorkloadName, sidecar.ApplicationAck = "worker", "failed"
	adoption.Targets = append(adoption.Targets, sidecar)
	adoption.Application.Current = 99 // derived from raw receipts
	report, err := Evaluate(inventory, policy, now)
	if err != nil || report.Passed || report.Bindings[0].ApplicationAdoption.Application.Failed != 1 || report.Bindings[0].ApplicationAdoption.Application.Current != 6 {
		t.Fatalf("mixed: %+v %v", report, err)
	}
	policy.RequireApplicationAck = false
	report, err = Evaluate(inventory, policy, now)
	if err != nil || !report.Passed {
		t.Fatalf("default changed: %+v %v", report, err)
	}
	inventory.Bindings[1].ApplicationAdoption = nil
	policy.RequireApplicationAck = true
	report, err = Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "application_adoption_unknown") {
		t.Fatalf("old server: %+v %v", report, err)
	}
}
