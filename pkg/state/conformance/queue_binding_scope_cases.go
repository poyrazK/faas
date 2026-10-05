package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testQueueBindingEnvironmentIdentity(t *testing.T, fx *Fixture) {
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "queue-scope-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fx.Store.CreateProjectEnvironment(fx.Ctx, state.ProjectEnvironment{AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "queue-scope-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	consumers := fx.Store.(state.QueueBindingConsumerStore)
	enqueue := func(scope, queue string) state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, DeploymentScope: scope, QueueName: queue, Payload: []byte(`{}`), DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	legacy := enqueue("staging", "orders")
	bind := func(scope, name string) state.QueueBindingConsumerResult {
		t.Helper()
		result, err := consumers.CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, DeploymentScope: scope, Name: name, QueueName: name, Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.Binding.DeploymentScope != scope {
			t.Fatal("binding lost scope")
		}
		if scope != "" {
			environment, err := fx.Store.ProjectEnvironmentBySlug(fx.Ctx, fx.Account.ID, project.ID, scope)
			if err != nil || result.Binding.EnvironmentID != environment.ID {
				t.Fatalf("binding lost catalog identity: %+v %v", result.Binding, err)
			}
		}
		trigger, err := fx.Store.TriggerByID(fx.Ctx, result.Changes[0].TriggerID)
		if err != nil || trigger.QueueBindingScope != scope || trigger.QueueBindingID.String() != uuid.MustParse(result.Binding.ID).String() || scope != "" && trigger.QueueBindingEnvironmentID.String() != uuid.MustParse(result.Binding.EnvironmentID).String() {
			t.Fatalf("consumer lost scope: %+v %v", trigger, err)
		}
		return result
	}
	prod, stage := bind("production", "orders"), bind("staging", "orders")
	production, staging := enqueue("", "orders"), enqueue("staging", "orders")
	if production.DeploymentScope != "production" || production.QueueBindingID != prod.Binding.ID || staging.QueueBindingID != stage.Binding.ID {
		t.Fatal("named admission crossed scopes")
	}
	for _, item := range []struct{ scope, id string }{{"production", prod.Binding.ID}, {"staging", stage.Binding.ID}} {
		if got := enqueue(item.scope, ""); got.QueueBindingID != item.id {
			t.Fatalf("unnamed admission selected wrong scoped consumer: %+v", got)
		}
	}
	if _, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, DeploymentScope: "staging", QueueBindingID: prod.Binding.ID, DueAt: time.Now()}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("cross-scope binding admitted: %v", err)
	}
	for _, inv := range []state.Invocation{legacy, production} {
		if _, err := fx.Store.InsertTriggerRecord(fx.Ctx, stage.Changes[0].TriggerID, inv.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("scoped consumer adopted foreign/legacy work: %v", err)
		}
	}
	receipt, err := fx.Store.InsertTriggerRecord(fx.Ctx, stage.Changes[0].TriggerID, staging.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []state.QueueBinding{prod.Binding, stage.Binding} {
		stats, err := fx.Store.QueueStateForBinding(fx.Ctx, app.ID, binding.ID)
		if err != nil || stats.Depth != 2 {
			t.Fatalf("scope backlog includes neighbor/legacy: %+v %v", stats, err)
		}
	}
	bind("staging", "payments") // Multiple named consumers are independent in one environment.
	if got := enqueue("staging", ""); got.QueueBindingID != "" {
		t.Fatal("ambiguous unnamed work selected a scoped consumer")
	}
	shared := bind("", "orders")
	if got := enqueue("production", "orders"); got.QueueBindingID != prod.Binding.ID {
		t.Fatal("legacy binding overrode exact environment")
	}
	if got := enqueue("preview", "orders"); got.QueueBindingID != shared.Binding.ID {
		t.Fatal("legacy shared binding lost compatibility")
	}
	if _, err := consumers.CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, DeploymentScope: "missing", Name: "missing", QueueName: "missing", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unregistered scope accepted: %v", err)
	}
	if _, err := consumers.DeleteQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, prod.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, DeploymentScope: "production", QueueName: "orders", DueAt: time.Now()}); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("retired exact binding fell back to shared: %v", err)
	}
	if got := enqueue("staging", "orders"); got.QueueBindingID != stage.Binding.ID {
		t.Fatal("neighbor retirement held staging")
	}
	if err := fx.Store.DeleteProjectEnvironment(fx.Ctx, fx.Account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	recreated, err := fx.Store.CreateProjectEnvironment(fx.Ctx, state.ProjectEnvironment{AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil || recreated.ID == stage.Binding.EnvironmentID {
		t.Fatalf("recreated environment reused identity: %+v %v", recreated, err)
	}
	replacement := bind("staging", "orders")
	if got := enqueue("staging", "orders"); got.QueueBindingID != replacement.Binding.ID {
		t.Fatal("replacement environment did not get its own binding")
	}
	disabled := false
	if _, err := consumers.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, shared.Binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if got := enqueue("staging", ""); got.QueueBindingID != replacement.Binding.ID {
		t.Fatal("unavailable original consumers made replacement admission ambiguous")
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, staging.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) {
		t.Fatalf("recreated slug released old environment work: %v", err)
	}
	if claims, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, stage.Changes[0].TriggerID, []string{staging.ID}); err != nil || len(claims) != 0 {
		t.Fatalf("recreated slug released old receipt: %+v %v", claims, err)
	}
	if err := fx.Store.DeleteProject(fx.Ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	for _, inv := range []state.Invocation{production, staging, legacy} {
		row, err := fx.Store.InvocationByID(fx.Ctx, inv.ID)
		if err != nil || row.DeploymentScope != inv.DeploymentScope || row.QueueBindingID != inv.QueueBindingID {
			t.Fatalf("membership removal changed accepted identity: %+v %v", row, err)
		}
	}
	row, err := fx.Store.QueueBindingHistoryByID(fx.Ctx, fx.Account.ID, app.ID, stage.Binding.ID)
	if err != nil || row.DeploymentScope != "staging" || row.EnvironmentID != stage.Binding.EnvironmentID {
		t.Fatalf("membership removal lost binding scope: %+v %v", row, err)
	}
	if id, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, stage.Changes[0].TriggerID, staging.ID); err != nil || id != receipt {
		t.Fatalf("scope lifecycle lost receipt: %q %v", id, err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, staging.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) {
		t.Fatalf("missing original environment released work: %v", err)
	}
	if claims, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, stage.Changes[0].TriggerID, []string{staging.ID}); err != nil || len(claims) != 0 {
		t.Fatalf("missing original environment released receipt: %+v %v", claims, err)
	}
}
