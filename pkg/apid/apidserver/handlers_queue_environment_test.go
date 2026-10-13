package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func scopedQueueHTTPFixture(t *testing.T) (apidserver.TestEnv, state.Project, state.App) {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "queue-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	return e, project, app
}

func createScopedHTTPQueue(t *testing.T, e apidserver.TestEnv, scope, name string) api.QueueBindingResponse {
	t.Helper()
	rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queue-bindings", api.CreateQueueBindingRequest{Environment: scope, Name: name, QueueName: name, Mode: "push", WorkloadClass: "worker"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s/%s: %d %s", scope, name, rec.Code, rec.Body)
	}
	var binding api.QueueBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding.Environment != scope || scope != "" && binding.EnvironmentID == "" || scope == "" && binding.EnvironmentID != "" {
		t.Fatalf("binding identity: %+v", binding)
	}
	return binding
}

func TestQueueEnvironmentHTTPSelectionPinsBindingAndFlagContext(t *testing.T) {
	e, project, app := scopedQueueHTTPFixture(t)
	production := createScopedHTTPQueue(t, e, "production", "orders")
	staging := createScopedHTTPQueue(t, e, "staging", "orders")
	createScopedHTTPQueue(t, e, "", "orders")
	tenant, _, err := (*e.Store()).CreatePlatformTenant(context.Background(), (*e.Acct()).ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	flagContext, err := flags.EncodePropagationHeader(flags.PropagationContext{Version: flags.PropagationContextVersion, CustomerID: tenant.ID, Decisions: []flags.PropagationDecision{{Decision: flags.Decision{Flag: "export", Value: true, ConfigVersion: 7, Reason: "default", Source: "configuration"}, Origin: flags.EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()}}}})
	if err != nil {
		t.Fatal(err)
	}
	// Other-environment queues cannot make the implicit production selection ambiguous.
	createScopedHTTPQueue(t, e, "staging", "payments")
	for _, tt := range []struct {
		name, scope, queue, binding string
		inbox                       bool
	}{
		{name: "default", binding: production.ID},
		{name: "production", scope: "production", queue: "orders", binding: production.ID},
		{name: "staging", scope: "staging", queue: "orders", binding: staging.ID},
		{name: "inbox", scope: "staging", queue: "orders", binding: staging.ID, inbox: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, want, body := "/v1/apps/queue-worker/queues/send", http.StatusCreated, any(api.QueueSendRequest{Environment: tt.scope, QueueName: tt.queue, Payload: json.RawMessage(`{"job":1}`), FlagContext: flagContext})
			if tt.inbox {
				path, want, body = "/v1/apps/queue-worker/inbox", http.StatusAccepted, api.SendAppMessageRequest{Environment: tt.scope, QueueName: tt.queue, Type: "order.created", Data: json.RawMessage(`{"job":1}`), FlagContext: flagContext}
			}
			rec := e.Do(t, http.MethodPost, path, body, nil)
			if rec.Code != want {
				t.Fatalf("send: %d %s", rec.Code, rec.Body)
			}
			var result struct {
				ID, Environment string
				BindingID       string `json:"queue_binding_id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			scope := tt.scope
			if scope == "" {
				scope = "production"
			}
			inv, err := (*e.Store()).InvocationByID(context.Background(), result.ID)
			if err != nil || inv.AppID != app.ID || inv.AccountID != (*e.Acct()).ID || inv.DeploymentScope != scope || inv.QueueBindingID != tt.binding || result.Environment != scope || result.BindingID != tt.binding {
				t.Fatalf("accepted selection: %+v %+v %v", result, inv, err)
			}
			assertInvocationFlagContext(t, inv, tenant.ID, flagContext)
		})
	}
	environment, err := (*e.Store()).ProjectEnvironmentBySlug(context.Background(), (*e.Acct()).ID, project.ID, "staging")
	if err != nil || environment.ID != staging.EnvironmentID {
		t.Fatalf("catalog identity: %+v %v", environment, err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queue-bindings", api.CreateQueueBindingRequest{Environment: "staging", Name: "orders", QueueName: "orders", Mode: "push"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeValidation)
}

func TestQueueEnvironmentHTTPRequiresOwnedEnabledBinding(t *testing.T) {
	e, _, _ := scopedQueueHTTPFixture(t)
	createScopedHTTPQueue(t, e, "", "orders")
	for _, scope := range []string{"staging", "missing", "Staging", "../production"} {
		rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: scope, QueueName: "orders", Payload: json.RawMessage(`{}`)}, nil)
		apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
	binding := createScopedHTTPQueue(t, e, "staging", "orders")
	disabled := false
	rec := e.Do(t, http.MethodPatch, "/v1/apps/queue-worker/queue-bindings/"+binding.ID, api.UpdateQueueBindingRequest{Enabled: &disabled}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", QueueName: "orders", Payload: json.RawMessage(`{}`)}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "legacy-queue", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker}); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/legacy-queue/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`)}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.Do(t, http.MethodPost, "/v1/apps/legacy-queue/queue-bindings", api.CreateQueueBindingRequest{Environment: "staging", Name: "orders", QueueName: "orders"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queue-bindings", api.CreateQueueBindingRequest{Environment: "missing", Name: "missing", QueueName: "missing"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestQueueEnvironmentHTTPRetirementCannotFallBackToShared(t *testing.T) {
	e, _, _ := scopedQueueHTTPFixture(t)
	createScopedHTTPQueue(t, e, "", "orders")
	production := createScopedHTTPQueue(t, e, "production", "orders")
	staging := createScopedHTTPQueue(t, e, "staging", "orders")
	rec := e.Do(t, http.MethodDelete, "/v1/apps/queue-worker/queue-bindings/"+production.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retire: %d %s", rec.Code, rec.Body)
	}
	for _, scope := range []string{"", "production"} {
		rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: scope, QueueName: "orders", Payload: json.RawMessage(`{}`)}, nil)
		apidserver.AssertProblem(t, rec, http.StatusConflict, "queue_binding_retired")
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`)}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("neighbor held: %d %s", rec.Code, rec.Body)
	}
	var got api.QueueSendResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.QueueBindingID != staging.ID {
		t.Fatalf("neighbor selection: %+v", got)
	}
}

func TestQueueEnvironmentHTTPRecreationHoldsOriginalWork(t *testing.T) {
	e, project, app := scopedQueueHTTPFixture(t)
	ctx := context.Background()
	original := createScopedHTTPQueue(t, e, "staging", "orders")
	rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`)}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var accepted api.QueueSendResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &accepted)
	if err := (*e.Store()).DeleteProjectEnvironment(ctx, (*e.Acct()).ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, http.MethodGet, "/v1/apps/queue-worker/queue-bindings/"+original.ID+"/status", nil, nil)
	var status api.QueueBindingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || rec.Code != http.StatusOK || status.ConsumerState != "paused" || status.ConsumerStateReason != "environment_unavailable" || status.ConsumerLiveness != "not_observed" || status.EnvironmentID != original.EnvironmentID || status.Depth != 1 {
		t.Fatalf("original status: %d %+v %v", rec.Code, status, err)
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", QueueName: "orders", Payload: json.RawMessage(`{}`)}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	replacement := createScopedHTTPQueue(t, e, "staging", "orders")
	if replacement.ID == original.ID || replacement.EnvironmentID == original.EnvironmentID {
		t.Fatal("replacement reused original identity")
	}
	if _, err := (*e.Store()).ClaimInvocationWithCap(ctx, accepted.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) {
		t.Fatalf("old work claim: %v", err)
	}
	stats, err := (*e.Store()).QueueStateForBinding(ctx, app.ID, replacement.ID)
	if err != nil || stats.Depth != 0 {
		t.Fatalf("replacement adopted old work: %+v %v", stats, err)
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`)}, nil)
	var got api.QueueSendResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusCreated || got.QueueBindingID != replacement.ID {
		t.Fatalf("replacement send: %d %+v", rec.Code, got)
	}
}

type recreateQueueEnvironmentOnAdmission struct {
	MemStore *state.MemStore
	before   func()
}

func (s *recreateQueueEnvironmentOnAdmission) EnqueueInvocation(ctx context.Context, inv state.Invocation) (state.Invocation, error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		before()
	}
	return s.MemStore.EnqueueInvocation(ctx, inv)
}

func TestQueueEnvironmentHTTPAdmissionRaceFailsClosedWithFlagContext(t *testing.T) {
	e, project, app := scopedQueueHTTPFixture(t)
	createScopedHTTPQueue(t, e, "staging", "orders")
	ctx := context.Background()
	tenant, _, err := (*e.Store()).CreatePlatformTenant(ctx, (*e.Acct()).ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	flagContext, err := flags.EncodePropagationHeader(flags.PropagationContext{Version: flags.PropagationContextVersion, CustomerID: tenant.ID, Decisions: []flags.PropagationDecision{{Decision: flags.Decision{Flag: "export", Value: true, ConfigVersion: 7, Reason: "default", Source: "configuration"}, Origin: flags.EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()}}}})
	if err != nil {
		t.Fatal(err)
	}
	(*(*e.S()).Store()) = statetest.NewMem(&recreateQueueEnvironmentOnAdmission{MemStore: (*e.Store()), before: func() {
		if err := (*e.Store()).DeleteProjectEnvironment(ctx, (*e.Acct()).ID, project.ID, "staging"); err != nil {
			t.Fatal(err)
		}
		if _, err := (*e.Store()).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
	}})
	rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`), FlagContext: flagContext}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, "queue_binding_environment_unavailable")
	n, err := (*e.Store()).CountPendingInvocations(ctx, app.ID, state.InvocationQueue)
	if err != nil || n != 0 {
		t.Fatalf("stale work admitted: %d %v", n, err)
	}
}

func TestQueueEnvironmentHTTPRejectsSharedKeyedWorkPolicy(t *testing.T) {
	e, _, app := scopedQueueHTTPFixture(t)
	createScopedHTTPQueue(t, e, "staging", "orders")
	rec := e.Do(t, http.MethodPost, "/v1/apps/queue-worker/queues/send", api.QueueSendRequest{Environment: "staging", Payload: json.RawMessage(`{}`), Work: &api.InvokeWork{Policy: "documents", Key: json.RawMessage(`"one"`)}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, "queue_environment_work_policy_unavailable")
	n, err := (*e.Store()).CountPendingInvocations(context.Background(), app.ID, state.InvocationQueue)
	if err != nil || n != 0 {
		t.Fatalf("rejected work admitted: %d %v", n, err)
	}
}

func TestQueueEnvironmentHTTPSelectsEnvironmentRelease(t *testing.T) {
	e, project, app := scopedQueueHTTPFixture(t)
	ctx := context.Background()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := (*e.Store()).UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "staging"} {
		createScopedHTTPQueue(t, e, scope, "orders")
		deployment, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, ImageDigest: "sha256:" + scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := (*e.Store()).MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		release, err := (*e.Store()).PublishProjectReleaseSet(ctx, (*e.Acct()).ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
		if err != nil {
			t.Fatal(err)
		}
		for _, inbox := range []bool{false, true} {
			path, want, body := "/v1/apps/queue-worker/queues/send", http.StatusCreated, any(api.QueueSendRequest{Environment: scope, Payload: json.RawMessage(`{}`)})
			if inbox {
				path, want, body = "/v1/apps/queue-worker/inbox", http.StatusAccepted, api.SendAppMessageRequest{Environment: scope, Type: "order.created", Data: json.RawMessage(`{}`)}
			}
			rec := e.Do(t, http.MethodPost, path, body, nil)
			var accepted api.QueueSendResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &accepted)
			inv, err := (*e.Store()).InvocationByID(ctx, accepted.ID)
			var headers map[string]string
			if decodeErr := json.Unmarshal(inv.Headers, &headers); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if rec.Code != want || err != nil || inv.DeploymentScope != scope || headers[api.ReleaseHeader] != release.ID || rec.Header().Get(api.ReleaseHeader) != release.ID {
				t.Fatalf("scoped version %s inbox=%t: %d %+v %v %s", scope, inbox, rec.Code, inv, err, rec.Body)
			}
			_, version, resolveErr := state.ResolveInvocationVersion(ctx, (*e.Store()), inv)
			if resolveErr != nil || version.DeploymentID != deployment.ID || version.ReleaseID != release.ID {
				t.Fatalf("captured release resolved wrong graph: %+v %v", version, resolveErr)
			}
		}
	}
}
