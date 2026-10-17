package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingInventoryQueueEnvironmentIsolation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "binding-scope"})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"staging", "app"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "binding-scope", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"", "production", "staging", "app"} {
		_, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, DeploymentScope: environment, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []string{"", "production", "staging", "app"} {
		inventory := apidserver.DecodeBindingInventory(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?scope="+scope, nil, nil))
		want := 2
		if scope == "" {
			want = 4
		}
		if len(inventory.Bindings) != want {
			t.Fatalf("scope %s: got %d queues, want %d", scope, len(inventory.Bindings), want)
		}
		seen := map[string]bool{}
		for _, item := range inventory.Bindings {
			if item.QueueEnvironment == nil || scope != "" && !api.QueueBindingInScope(item, scope) {
				t.Fatalf("queue lost environment: %+v", item)
			}
			seen[*item.QueueEnvironment] = true
		}
		if !seen[""] || scope != "" && !seen[scope] {
			t.Fatalf("shared/scoped binding missing: %v", seen)
		}
	}
}

func TestBindingPromotionQueueRequirementEnforcesHealthyConsumer(t *testing.T) {
	e, app, serving, candidate := apidserver.PromotionFixture(t)
	ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
	queue, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTriggerConsumerHealth(ctx, queue.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().UTC(), Success: true}); err != nil {
		t.Fatal(err)
	}
	apidserver.CompletePromotionProbe(t, e, app, candidate, apidserver.PassedPostgresVerification)
	zero := int64(0)
	base := "/v1/apps/" + app.Slug + "/bindings/release-policy"
	response := e.Do(t, http.MethodPut, base, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}, nil)
	if response.Code != 200 {
		t.Fatalf("set strict policy: %d %s", response.Code, response.Body.String())
	}
	promote := "/v1/deployments/" + candidate.ID + "/promote"
	response = e.Do(t, http.MethodPost, promote, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "verification_unsupported") {
		t.Fatalf("default strict policy weakened: %d %s", response.Code, response.Body.String())
	}
	one := int64(1)
	requirements := map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
	response = e.Do(t, http.MethodPut, base+"-with-queues", api.SetQueueBindingReleasePolicyRequest{SetBindingReleasePolicyRequest: api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &one}, QueueRequirements: requirements}, nil)
	if response.Code != 200 {
		t.Fatalf("set explicit readiness policy: %d %s", response.Code, response.Body.String())
	}
	two := int64(2)
	for _, path := range []string{base, base + "-with-object-storage", base + "-with-queues"} {
		response = e.Do(t, http.MethodPut, path, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &two}, nil)
		if response.Code != 400 {
			t.Fatalf("implicit requirement removal on %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if err := store.RecordTriggerConsumerHealth(ctx, queue.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().Add(-time.Minute), Success: true}); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, promote, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "queue_consumer_stale") {
		t.Fatalf("readiness policy waived stale poll: %d %s", response.Code, response.Body.String())
	}
	apidserver.AssertPromotionWeights(t, e, serving, candidate, 0)
	if err := store.RecordTriggerConsumerHealth(ctx, queue.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().UTC(), Success: true}); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, promote, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	var receipt api.BindingPromotionResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.BindingsCheck == nil || !receipt.BindingsCheck.Passed || receipt.BindingsCheck.AllowUnsupported || receipt.BindingsCheck.Coverage != "partial" || receipt.BindingsCheck.QueueRequirements["orders"] != api.QueueEvidenceConsumerReadiness {
		t.Fatalf("readiness promotion receipt: %d %s", response.Code, response.Body.String())
	}
	apidserver.AssertPromotionWeights(t, e, serving, candidate, 100)
}

func TestBindingPromotionQueueDeadlineIgnoresOtherEnvironments(t *testing.T) {
	now := time.Now().UTC()
	production, staging, namedApp := "production", "staging", "app"
	fresh, expired := now.Add(-time.Second), now.Add(-time.Minute)
	inventory := api.AppBindingInventory{Bindings: []api.AppBindingInventoryItem{
		{Type: api.BindingTypeQueue, Binding: "orders", Scope: production, QueueEnvironment: &production, State: "enabled", Access: "push", ObservedAt: &fresh},
		{Type: api.BindingTypeQueue, Binding: "orders", Scope: staging, QueueEnvironment: &staging, State: "enabled", Access: "push", ObservedAt: &expired},
		{Type: api.BindingTypeQueue, Binding: "orders", Scope: namedApp, QueueEnvironment: &namedApp, State: "enabled", Access: "push", ObservedAt: &expired},
	}}
	deadline, blocker := apidserver.BindingPromotionDeadline(api.BindingCheckReport{Scope: production}, inventory, time.Minute)
	if !deadline.Equal(fresh.Add(api.QueueConsumerMaxPollAge)) || blocker.Scope != production {
		t.Fatalf("unrelated queue shortened deadline: %v %+v", deadline, blocker)
	}
}

func TestBindingMigrationAudienceDoesNotAdmitManualVerification(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, deployment := apidserver.SeedAppTaskDeployment(t, e, "migration-audience")
	catalog, runtime := apidserver.ReadyVerificationBinding(t, e, app)
	ctx, now := context.Background(), time.Now().UTC()
	migration, _, err := catalog.ReserveBinding(ctx, managedpostgres.Binding{ID: uuid.NewString(), AccountID: runtime.AccountID, DatabaseID: runtime.DatabaseID, AppID: app.ID, Scope: runtime.Scope, EnvironmentKey: "MIGRATION_DATABASE_URL", Access: managedpostgres.CredentialMigration, CredentialGeneration: 1, State: managedpostgres.BindingStateProvisioning, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := catalog.ClaimBinding(ctx, migration.AccountID, migration.ID, token, managedpostgres.BindingStateProvisioning, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.FinishBindingProvision(ctx, migration.ID, token, "migration-provider", "migration-secret", now); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.CompletePromotionProbe(t, e, app, deployment, apidserver.PassedPostgresVerification)
	inventory := apidserver.DecodeBindingInventory(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?deployment_id="+deployment.ID, nil, nil))
	report, err := bindingcheck.Evaluate(inventory, bindingcheck.Policy{App: app.Slug, DeploymentID: deployment.ID, MaxVerificationAge: time.Minute}, time.Now())
	if err != nil || !report.Passed {
		t.Fatalf("release-only binding blocked runtime gate: %+v %v", report, err)
	}
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "MIGRATION_DATABASE_URL"}, VerificationDeploymentID: deployment.ID}, nil)
	if response.Code != 403 {
		t.Fatalf("release credential admitted as manual verification: %d %s", response.Code, response.Body.String())
	}
}
