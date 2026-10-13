package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConfigureQueueWorkloadCreatesAndConverges(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-profile", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodPut, "/v1/apps/queue-profile/queue-workload", api.QueueWorkloadProfileRequest{}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got api.QueueWorkloadProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got.Binding.Name != "default" || got.Binding.QueueName != "default" || got.Binding.Mode != "push" || got.Binding.WorkloadClass != "worker" || !got.Binding.Enabled || got.Binding.MaxConcurrency != 1 {
		t.Fatalf("binding = %+v", got.Binding)
	}
	if got.ScalingPolicy == nil || got.ScalingPolicy.Target == nil || got.ScalingPolicy.Target.Metric != "queue_depth" || got.ScalingPolicy.Target.Value != 10 {
		t.Fatalf("scaling policy = %+v", got.ScalingPolicy)
	}
	if got.App.Slug != app.Slug || got.App.WorkloadClass != "worker" {
		t.Fatalf("app response = %+v", got.App)
	}
	triggerID, err := apidserver.QueueBindingTriggerID(ctx, (*e.Store()), app.ID, got.Binding.ID)
	if err != nil || triggerID == "" {
		t.Fatalf("consumer projection = %q, err=%v", triggerID, err)
	}

	stored, err := (*e.Store()).AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScalingPolicy == nil || stored.ScalingPolicy.Target == nil || stored.ScalingPolicy.Target.Metric != "queue_depth" {
		t.Fatalf("stored scaling policy = %+v", stored.ScalingPolicy)
	}

	rec = e.Do(t, http.MethodPut, "/v1/apps/queue-profile/queue-workload", api.QueueWorkloadProfileRequest{
		QueueName: "orders", TargetDepth: 5, MaxConcurrency: 3, Force: true,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("converge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueWorkloadProfileResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode converge response: %v", err)
	}
	if got.Created || got.Binding.QueueName != "orders" || got.Binding.MaxConcurrency != 3 || got.ScalingPolicy == nil || got.ScalingPolicy.Target == nil || got.ScalingPolicy.Target.Value != 5 {
		t.Fatalf("converged profile = %+v", got)
	}
	bindings, err := (*e.Store()).ListQueueBindingsForApp(ctx, (*e.Acct()).ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].QueueName != "orders" {
		t.Fatalf("bindings after converge = %+v", bindings)
	}
}

func TestConfigureQueueWorkloadRejectsQueueMoveWithoutForce(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-conflict", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "default", QueueName: "payments",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodPut, "/v1/apps/queue-conflict/queue-workload", api.QueueWorkloadProfileRequest{QueueName: "orders"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	stored, err := (*e.Store()).AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScalingPolicy != nil {
		t.Fatalf("scaling policy changed on conflict: %+v", stored.ScalingPolicy)
	}
}

func TestConfigureQueueWorkloadIsPlanGated(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-free", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPut, "/v1/apps/queue-free/queue-workload", api.QueueWorkloadProfileRequest{}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	bindings, err := (*e.Store()).ListQueueBindingsForApp(context.Background(), (*e.Acct()).ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("free-plan bindings = %+v, want none", bindings)
	}
}

func TestConfigureQueueWorkloadKeepsScopedDefaultsSeparate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "queue-profile-scopes"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "queue-profile-scopes", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := (*e.Store()).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: (*e.Acct()).ID, AppID: app.ID, DeploymentScope: "production", Name: "default", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		rec := e.Do(t, http.MethodPut, "/v1/apps/queue-profile-scopes/queue-workload", api.QueueWorkloadProfileRequest{QueueName: "orders", MaxConcurrency: 1}, nil)
		if rec.Code != status {
			t.Fatalf("profile status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
		}
		var got api.QueueWorkloadProfileResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Binding.ID == scoped.Binding.ID || got.Binding.MaxConcurrency != 1 {
			t.Fatalf("legacy profile selected scoped default: %+v", got.Binding)
		}
	}
	current, err := (*e.Store()).QueueBindingByID(ctx, (*e.Acct()).ID, app.ID, scoped.Binding.ID)
	if err != nil || current.DeploymentScope != "production" || current.EnvironmentID != scoped.Binding.EnvironmentID || current.MaxConcurrency != 3 {
		t.Fatalf("legacy profile changed scoped binding: %+v %v", current, err)
	}
}

// `gregale queue setup` sends no workload class. For an http app the refusal
// named workload_class, a field the caller never set (production-us hunt #5,
// H5-27); it now says what the command supports and what to use instead.
func TestConfigureQueueWorkloadExplainsHTTPApps(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-http", WorkloadClass: state.WorkloadClassHTTP,
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPut, "/v1/apps/queue-http/queue-workload", api.QueueWorkloadProfileRequest{}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "worker and job apps") || !strings.Contains(body, "queue bindings create") {
		t.Fatalf("problem does not explain the alternative: %s", body)
	}
}
