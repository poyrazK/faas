// adr: 20261022183756
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkloadActionHistoryMCPReceipts(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
			for _, source := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/source=%t", protocol, mode, source), func(t *testing.T) {
					f, _, _ := newReleaseFixture(t, true)
					f.app.Manifest.ExecutionMode = mode
					backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
					t.Cleanup(backend.Close)
					cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, protocol)
					tool, args := "gregale_plan_workload_rollout", workloadPlanArgs()
					if source {
						tool, args = "gregale_plan_source_deploy", sourcePlanArgs()
						args["rollout_mode"] = mode
					}
					args["reason"] = "PRIVATE_WORKLOAD_REASON"
					id := planID(t, callTool(t, ctx, cs, tool, args, false))
					f.mu.Lock()
					f.ambiguous = true
					f.mu.Unlock()
					callTool(t, ctx, cs, "gregale_apply_workload_rollout", applyArgs{PlanID: id}, true)
					out := callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, false)
					var action actionView
					if json.Unmarshal(out["action"], &action) != nil || action.Workload == nil || action.Workload.Mode != mode || action.Apply.State != "outcome_unknown" || action.RetryTool != "gregale_apply_workload_rollout" || action.Observation != nil {
						t.Fatalf("lost response fabricated a generation or lost its retry handle: %s", out)
					}
					callTool(t, ctx, cs, "gregale_apply_workload_rollout", applyArgs{PlanID: id}, false)
					out = callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, false)
					if json.Unmarshal(out["action"], &action) != nil || action.Apply.State != "accepted" || action.Apply.Receipt.DeploymentID != "release-new" || action.Promotion != nil || action.NextStep != "observe_native_rollout" || action.Observation == nil || action.Observation.Tool != "gregale_get_rollout" || action.Observation.Arguments.DeploymentID != "release-new" || action.Observation.Arguments.App != "alpha" || action.Observation.Arguments.Environment != "" || string(out["native_completion"]) != `"not_observed"` {
						t.Fatalf("accepted activation overstated completion or lost exact observation: %s", out)
					}
					encoded, _ := json.Marshal(out)
					for _, private := range []string{"PRIVATE_WORKLOAD_REASON", testImage, "team/repository", strings.Repeat("a", 40), "configuration_hash", "rollout_policy", "mcp-workload-"} {
						if strings.Contains(string(encoded), private) {
							t.Fatalf("workload history leaked plan data: %s", private)
						}
					}
					page := callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
					var items []actionView
					if json.Unmarshal(page["items"], &items) != nil || len(items) != 1 || items[0].PlanID != id {
						t.Fatal("workload plan is not discoverable")
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != 1 {
						t.Fatalf("history inspection or replay created another generation: %d", f.writes)
					}
				})
			}
		}
	}
}

func TestWorkloadActionHistoryServiceAbortRestartAndExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expire), func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = api.ExecutionModeService
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(backend.Close)
			store, path, key := testStateStore(t)
			opts := Options{APIBase: backend.URL, AllowWrites: true, planStore: store, planNamespace: "abort-history"}
			client := api.NewClient(backend.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			rollout, err := m.planWorkloadRollout(t.Context(), planWorkloadRolloutArgs{App: "alpha", Image: testImage, Reason: "activate service"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.applyWorkloadRollout(t.Context(), applyArgs{PlanID: rollout.(releasePlan).ID}); err != nil {
				t.Fatal(err)
			}
			plan, err := m.planServiceAbort(t.Context(), planServiceAbortArgs{App: "alpha", DeploymentID: "release-new", PredecessorDeploymentID: "release-base", Reason: "PRIVATE_ABORT_REASON"})
			if err != nil {
				t.Fatal(err)
			}
			id := plan.(releasePlan).ID
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyServiceAbort(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost response fixture succeeded")
			}
			if expire {
				m.mu.Lock()
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
				err = m.persistPlans()
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = next.Close() })
			opts.planStore = next
			_, restored, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			detail := actionDetail(t, restored, getActionArgs{App: "alpha", PlanID: id})
			w := detail.Action.Workload
			if w == nil || w.Mode != api.ExecutionModeService || w.DeploymentID != "release-new" || w.PredecessorDeploymentID != "release-base" || detail.Action.Apply.State != "outcome_unknown" || detail.Action.Observation == nil || detail.Action.Observation.Arguments.DeploymentID != w.DeploymentID || detail.NativeCompletion != "not_observed" {
				t.Fatalf("restart lost exact recovery pair or invented completion: %+v", detail)
			}
			if expire {
				if detail.Action.PlanAvailable || detail.Action.RetryTool != "" || detail.Action.NextStep != "inspect_native_state_before_replanning" {
					t.Fatal("expired abort history granted retry authority")
				}
				if _, err := restored.applyServiceAbort(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("expired recovery plan was applied")
				}
			} else {
				if detail.Action.RetryTool != "gregale_apply_service_abort" {
					t.Fatal("recovery hint selected the wrong native mutation")
				}
				for range 2 {
					if _, err := restored.applyServiceAbort(t.Context(), applyArgs{PlanID: id}); err != nil {
						t.Fatal(err)
					}
				}
				detail = actionDetail(t, restored, getActionArgs{App: "alpha", PlanID: id})
				if detail.Action.Apply.State != "accepted" || detail.Action.Apply.Receipt.RecoveryRequestID != "recovery-one" || detail.Action.Apply.Receipt.HandoffGeneration != 3 || detail.Action.NextStep != "observe_native_rollout" {
					t.Fatal("replayed reverse handoff lost its receipt")
				}
			}
			opts.planNamespace = "another-grant"
			_, other, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil || len(actionPage(t, other, listActionsArgs{App: "alpha"}).Items) != 0 {
				t.Fatal("another grant inherited workload history")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 2 || len(f.recoveries) != 1 {
				t.Fatalf("history or restart repeated reverse-handoff intent: writes=%d receipts=%d", f.writes, len(f.recoveries))
			}
		})
	}
}

func TestWorkloadActionHistoryNamedConsentAndExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expire), func(t *testing.T) {
			f, backend, opts := namedStageOptions(t)
			f.mode = api.ExecutionModeWorker
			store, path, key := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "named-workload-history"
			client := api.NewClient(backend.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := m.planWorkloadRollout(t.Context(), planWorkloadRolloutArgs{App: "alpha", Project: "shop", Environment: "staging", Image: testImage, Reason: "PRIVATE_NAMED_REASON"})
			if err != nil {
				t.Fatal(err)
			}
			id := plan.(releasePlan).ID
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyWorkloadRollout(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost named activation response succeeded")
			}
			if expire {
				m.mu.Lock()
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
				err = m.persistPlans()
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = next.Close() })
			opts.planStore = next
			grant := opts.Environments.Named["shop/staging"]
			grant.Write = false
			opts.Environments.Named["shop/staging"] = grant
			_, restored, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			args := getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
			detail := actionDetail(t, restored, args)
			if detail.Action.Apply.State != "outcome_unknown" || detail.Action.PlanAvailable == expire || expire && detail.Action.RetryTool != "" {
				t.Fatal("restart changed native uncertainty or extended expiry")
			}
			if _, err := restored.applyWorkloadRollout(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("historical retry hint bypassed current write consent or expiry")
			}
			if _, err := restored.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
				t.Fatal("named history leaked into the default environment")
			}
			if !expire {
				grant.Write = true
				opts.Environments.Named["shop/staging"] = grant
				_, restored, err = newManagementServer(client, opts)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := restored.applyWorkloadRollout(t.Context(), applyArgs{PlanID: id}); err != nil {
					t.Fatal(err)
				}
				detail = actionDetail(t, restored, args)
				if detail.Action.Observation == nil || detail.Action.Observation.Arguments.Environment != "staging" || detail.Action.Observation.Arguments.Project != "shop" || detail.Action.Observation.Arguments.DeploymentID != "stage-new" || detail.NativeCompletion != "not_observed" {
					t.Fatal("rollout observation lost immutable named context")
				}
			}
			result, err := restored.getAction(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.envID = "replacement-environment"
			f.mu.Unlock()
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("in-flight environment replacement disclosed workload history")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatalf("unconsented/expired replay or history changed native state: %d", f.writes)
			}
		})
	}
}

func TestWorkloadActionHistoryCompatibilityAndCombinedBound(t *testing.T) {
	f, _, _ := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeService
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	store, _, _ := testStateStore(t)
	opts := Options{APIBase: backend.URL, AllowWrites: true, planStore: store, planNamespace: "compat-history"}
	client := api.NewClient(backend.URL, testToken)
	_, m, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.planWorkloadRollout(t.Context(), planWorkloadRolloutArgs{App: "alpha", Image: testImage, Reason: "review"})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	var before actionHistorySnapshot
	if err := store.read(workloadActionsNamespace+opts.planNamespace, &before); err != nil || len(before.Items) != 1 {
		t.Fatal("workload history did not use its additive namespace")
	}
	// N-1 can fill its own history while ignoring retained companion records.
	now := time.Now().UTC()
	legacy := actionHistorySnapshot{Version: 1, Items: make(map[string]actionRecord)}
	for i := range api.ManagementMCPActionHistoryMax {
		p := &releasePlan{ID: fmt.Sprintf("%064x", i+1), Action: "rollback", APIBase: backend.URL, AccountID: "account-one", App: "alpha", AppID: "app-one", Scope: "default", ExpiresAt: now.Add(-time.Minute)}
		e := recordAction(p)
		e.RecordedAt, e.UpdatedAt = now.Add(-time.Hour-time.Duration(i)*time.Second), now.Add(-time.Minute)
		legacy.Items[p.ID] = e
	}
	if err := store.update("actions/"+opts.planNamespace, legacy); err != nil {
		t.Fatal(err)
	}
	_, m, err = newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.actions) != api.ManagementMCPActionHistoryMax || m.actions[id].PlanID != id {
		t.Fatal("combined history exceeded capacity or evicted the live plan")
	}
	legacy = actionHistorySnapshot{}
	if err := store.read("actions/"+opts.planNamespace, &legacy); err != nil || len(legacy.Items) != api.ManagementMCPActionHistoryMax-1 {
		t.Fatal("combined pruning did not persist")
	}
	for _, e := range legacy.Items {
		if e.Action != "deploy" && e.Action != "rollback" && e.Action != "regression_action" || e.Workload != nil {
			t.Fatal("N-1 history contains an unfamiliar action")
		}
	}
	if _, err := m.applyWorkloadRollout(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	// Emulate N-1 committing the original replay receipt without updating its
	// unfamiliar companion namespace. Upgrade reconciles only the live plan.
	if err := store.update(workloadActionsNamespace+opts.planNamespace, before); err != nil {
		t.Fatal(err)
	}
	_, upgraded, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	detail := actionDetail(t, upgraded, getActionArgs{App: "alpha", PlanID: id})
	if detail.Action.Apply.State != "accepted" || detail.Action.Apply.Receipt.DeploymentID != "release-new" || !detail.Action.RecordedAt.Equal(before.Items[id].RecordedAt) {
		t.Fatal("upgrade lost original identity/time or replay receipt")
	}
	if err := store.update("oauth", oauthSnapshot{}); err != nil {
		t.Fatal(err)
	}
	var removed actionHistorySnapshot
	if err := store.read(workloadActionsNamespace+opts.planNamespace, &removed); err != nil || len(removed.Items) != 0 {
		t.Fatal("revocation retained companion history")
	}
}

func TestWorkloadActionHistoryRejectsMismatchedRecoveryReceipts(t *testing.T) {
	p := &releasePlan{Action: "service_abort", RolloutMode: api.ExecutionModeService, AppID: "app-one", Scope: "default", TargetDeploymentID: "candidate", PredecessorDeploymentID: "predecessor", attempted: true,
		result: &api.DeploymentResponse{ID: "candidate", AppID: "app-one", Scope: "default"}}
	for _, receipt := range []api.ServiceRolloutRecoveryReceipt{
		{DeploymentID: "other", PredecessorDeploymentID: "predecessor", RequestID: "request-one", Status: "accepted"},
		{DeploymentID: "candidate", PredecessorDeploymentID: "other", RequestID: "request-one", Status: "accepted"},
		{DeploymentID: "candidate", PredecessorDeploymentID: "predecessor", RequestID: "../private", Status: "accepted"},
		{DeploymentID: "candidate", PredecessorDeploymentID: "predecessor", RequestID: "request-one", Status: "completed"},
	} {
		p.ServiceRecovery = &receipt
		if phase := workloadActionPhase(p); phase.State != "outcome_unknown" || phase.Receipt != nil {
			t.Fatal("unbound recovery receipt was published as accepted")
		}
	}
	p.ServiceRecovery = &api.ServiceRolloutRecoveryReceipt{DeploymentID: "candidate", PredecessorDeploymentID: "predecessor", RequestID: "request-one", Status: "accepted"}
	p.result.Error = "PRIVATE_NATIVE_ERROR"
	p.result.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Generation: 4, PredecessorDeploymentID: "predecessor", Reason: "PRIVATE_NATIVE_REASON", LastError: "PRIVATE_NATIVE_ERROR", ExpectedGateways: []string{"PRIVATE_GATEWAY"}}
	phase := workloadActionPhase(p)
	encoded, _ := json.Marshal(phase)
	if phase.State != "accepted" || phase.Receipt.HandoffGeneration != 4 || strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatal("accepted recovery projection lost its generation or exposed native details")
	}
}

func TestWorkloadActionHistoryRejectsInvalidSnapshots(t *testing.T) {
	now := time.Now().UTC()
	p := &releasePlan{ID: strings.Repeat("a", 64), Action: "service_abort", APIBase: "https://api.example", AccountID: "account-one", App: "alpha", AppID: "app-one", Scope: "default", RolloutMode: api.ExecutionModeService, TargetDeploymentID: "candidate", PredecessorDeploymentID: "predecessor", ExpiresAt: now.Add(-time.Second)}
	entry := recordAction(p)
	entry.RecordedAt, entry.UpdatedAt = now.Add(-time.Minute), now.Add(-time.Minute)
	for _, tc := range []struct {
		name      string
		namespace string
		version   int
		mutate    func(*actionRecord)
	}{
		{name: "version", namespace: workloadActionsNamespace, version: 2},
		{name: "old-reader-namespace", namespace: "actions/", version: 1},
		{name: "missing-pair", namespace: workloadActionsNamespace, version: 1, mutate: func(e *actionRecord) { e.Workload.PredecessorDeploymentID = "" }},
		{name: "worker-abort", namespace: workloadActionsNamespace, version: 1, mutate: func(e *actionRecord) { e.Workload.Mode = api.ExecutionModeWorker }},
		{name: "wrong-generation", namespace: workloadActionsNamespace, version: 1, mutate: func(e *actionRecord) {
			e.Apply = actionPhase{State: "accepted", Receipt: &actionReceipt{DeploymentID: "other", RecoveryRequestID: "request-one"}}
		}},
		{name: "negative-generation", namespace: workloadActionsNamespace, version: 1, mutate: func(e *actionRecord) {
			e.Apply = actionPhase{State: "accepted", Receipt: &actionReceipt{DeploymentID: "candidate", RecoveryRequestID: "request-one", HandoffGeneration: -1}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			encoded, _ := json.Marshal(entry)
			var altered actionRecord
			if err := json.Unmarshal(encoded, &altered); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(&altered)
			}
			if err := store.update(tc.namespace+"invalid", actionHistorySnapshot{Version: tc.version, Items: map[string]actionRecord{altered.PlanID: altered}}); err != nil {
				t.Fatal(err)
			}
			if _, err := NewServer(api.NewClient(p.APIBase, testToken), Options{APIBase: p.APIBase, planStore: store, planNamespace: "invalid"}); err == nil {
				t.Fatal("invalid workload history snapshot was exposed")
			}
		})
	}
}
