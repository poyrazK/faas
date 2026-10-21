package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func managedQueueCanaryFixture(t *testing.T) (*MemStore, App, Deployment, QueueBindingConsumerResult) {
	t.Helper()
	m, app, dep, _, _ := managedProbeFixture(t)
	frozen, _ := dep.ScopedWorkloadRuntime()
	app.Type, app.WorkloadClass, app.Manifest.ExecutionMode = AppTypeApp, WorkloadClassWorker, api.ExecutionModeWorker
	app.Manifest.RevisionPinTTLSeconds = 0
	m.apps[app.ID] = app
	m.accounts[app.AccountID] = Account{ID: app.AccountID, Plan: api.PlanPro}
	m.projectEnvironments[frozen.EnvironmentID] = ProjectEnvironment{ID: frozen.EnvironmentID, AccountID: app.AccountID, ProjectID: app.ProjectID, Slug: dep.Scope}
	queue, err := m.CreateQueueBindingWithConsumer(t.Context(), QueueBinding{ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentScope: dep.Scope, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	frozen.WorkloadClass = WorkloadClassWorker
	frozen.Runtime["execution_mode"] = json.RawMessage(`"worker"`)
	frozen.Variables = map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`}
	frozen.QueueBindings = map[string]EnvironmentScopedQueueBinding{"orders": {BindingID: queue.Binding.ID, TriggerID: queue.Changes[0].TriggerID, Contract: gitOpsQueueContract(queue.Binding)}}
	frozen.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"smoke":true}`)}}
	raw, _ := json.Marshal(frozen)
	dep.EnvironmentWorkloadRuntime = string(raw)
	dep.TrafficPercent = 0
	m.deployments[dep.ID] = dep
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("invalid managed canary fixture: %v", err)
	}
	return m, app, dep, queue
}

func TestManagedQueueCanaryFences(t *testing.T) {
	for _, scenario := range []string{"release", "held", "scope", "suspended", "detached", "source-mode", "consumer", "trigger", "capability", "class", "policy", "service-policy", "runtime"} {
		t.Run(scenario, func(t *testing.T) {
			m, app, dep, queue := managedQueueCanaryFixture(t)
			req := api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true}
			allowed, reason, err := m.QueueBindingCanaryEligibility(t.Context(), app.AccountID, app.ID, dep.ID, "orders")
			if err != nil || !allowed || reason != "" {
				t.Fatalf("eligibility: %v %s %v", allowed, reason, err)
			}
			r, err := m.CreateQueueBindingCanary(t.Context(), app.AccountID, app.ID, req)
			if err != nil || r.ReleaseID == "" {
				t.Fatalf("publish: %+v %v", r, err)
			}
			inv := m.invocations[r.ID]
			now := time.Now()
			inv.State = InvocationDispatching
			inv.Attempts = 1
			inv.ReceivedAt = &now
			m.invocations[inv.ID] = inv
			if err := validateQueueCanaryDispatch(t.Context(), m, app, inv, true); err != nil {
				t.Fatal(err)
			}
			changed := inv
			changed.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: dep.ID})
			if err := validateQueueCanaryDispatch(t.Context(), m, app, changed, true); err == nil {
				t.Fatal("worker canary fell back to HTTP revision pin")
			}
			frozen, _ := dep.ScopedWorkloadRuntime()
			switch scenario {
			case "release":
				old := m.projectReleaseSets[r.ReleaseID]
				old.Active = false
				m.projectReleaseSets[old.ID] = old
				old.ID = uuid.NewString()
				old.Active = true
				m.projectReleaseSets[old.ID] = old
				m.activeProjectReleaseSets[releaseKey(app.ProjectID, dep.Scope)] = old.ID
			case "held":
				dep.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(true)
			case "scope":
				dep.Scope = "staging"
			case "suspended":
				m.environmentGitOps[frozen.SourceID].source.Suspended = true
			case "detached":
				m.environmentGitOps[frozen.SourceID].source.Detached = true
			case "source-mode":
				m.environmentGitOps[frozen.SourceID].source.Spec.Mode = "audit"
			case "consumer":
				q := m.queueBindings[queue.Binding.ID]
				q.MaxConcurrency++
				m.queueBindings[q.ID] = q
			case "trigger":
				for id, tr := range m.triggers {
					if tr.ID.String() == queue.Changes[0].TriggerID {
						tr.Enabled = false
						m.triggers[id] = tr
					}
				}
			case "capability":
				frozen.Variables = nil
			case "class":
				frozen.WorkloadClass = WorkloadClassJob
			case "runtime":
				frozen.Variables["REVIEWED"] = "changed"
			case "policy":
				if m.bindingReleasePolicies == nil {
					m.bindingReleasePolicies = map[bindingReleasePolicyKey]api.BindingReleasePolicy{}
				}
				p := m.bindingReleasePolicyLocked(app.ID, dep.Scope)
				p.Revision++
				m.bindingReleasePolicies[bindingReleasePolicyKey{app.ID, dep.Scope}] = p
			case "service-policy":
				app.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
				m.apps[app.ID] = app
			}
			raw, _ := json.Marshal(frozen)
			dep.EnvironmentWorkloadRuntime = string(raw)
			m.deployments[dep.ID] = dep
			got, err := m.GetQueueBindingCanary(t.Context(), app.AccountID, app.ID, r.ID)
			if err != nil || got.Status != "blocked" {
				t.Fatalf("stale evidence: %+v %v", got, err)
			}
			if err := validateQueueCanaryDispatch(t.Context(), m, app, inv, true); err == nil {
				t.Fatal("stale canary dispatched")
			}
			if got, err := m.CancelQueueBindingCanary(t.Context(), app.AccountID, app.ID, r.ID); err != nil || got.Status != "cancelled" {
				t.Fatalf("cancel: %+v %v", got, err)
			}
		})
	}
}
