//go:build !no_pg

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

type recreatePGQueueEnvironmentOnAdmission struct {
	*state.PgStore
	before func()
}

func (s *recreatePGQueueEnvironmentOnAdmission) EnqueueInvocation(ctx context.Context, inv state.Invocation) (state.Invocation, error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		before()
	}
	return s.PgStore.EnqueueInvocation(ctx, inv)
}

func TestPGQueueEnvironmentHTTPIdentityAndAdmissionRace(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	ctx := context.Background()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "scoped-queue"})
	if err != nil {
		t.Fatal(err)
	}
	createEnvironment := func() {
		t.Helper()
		if _, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
	}
	createEnvironment()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "scoped-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/scoped-worker"
	create := func(scope string) api.QueueBindingResponse {
		t.Helper()
		rec := e.do(t, http.MethodPost, base+"/queue-bindings", api.CreateQueueBindingRequest{Environment: scope, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: "worker"}, nil)
		var binding api.QueueBindingResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil || rec.Code != http.StatusCreated || binding.Environment != scope {
			t.Fatalf("binding %s: %d %s %v", scope, rec.Code, rec.Body, err)
		}
		return binding
	}
	production, staging := create("production"), create("staging")
	create("")
	var stagingInvocation string
	for _, scope := range []string{"", "staging"} {
		rec := e.do(t, http.MethodPost, base+"/queues/send", api.QueueSendRequest{Environment: scope, Payload: json.RawMessage(`{}`)}, nil)
		var accepted api.QueueSendResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("send %s: %d %s %v", scope, rec.Code, rec.Body, err)
		}
		want := production
		if scope == "staging" {
			want = staging
			stagingInvocation = accepted.ID
		}
		inv, err := e.store.InvocationByID(ctx, accepted.ID)
		if err != nil || accepted.QueueBindingID != want.ID || inv.QueueBindingID != want.ID || inv.DeploymentScope != want.Environment {
			t.Fatalf("scope admission: %+v %+v %v", accepted, inv, err)
		}
	}
	rec := e.do(t, http.MethodDelete, base+"/queue-bindings/"+production.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retire: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodPost, base+"/queues/send", api.QueueSendRequest{QueueName: "orders", Payload: json.RawMessage(`{}`)}, nil)
	assertProblem(t, rec, http.StatusConflict, "queue_binding_retired")
	if err := e.store.DeleteProjectEnvironment(ctx, e.acct.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	createEnvironment()
	rec = e.do(t, http.MethodGet, base+"/queue-bindings/"+staging.ID+"/status", nil, nil)
	var status api.QueueBindingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || rec.Code != http.StatusOK || status.EnvironmentID != staging.EnvironmentID || status.ConsumerStateReason != "environment_unavailable" || status.Depth != 1 {
		t.Fatalf("original status: %d %+v %v", rec.Code, status, err)
	}
	replacement := create("staging")
	if replacement.EnvironmentID == staging.EnvironmentID {
		t.Fatal("recreated environment reused identity")
	}
	if _, err := e.store.ClaimInvocationWithCap(ctx, stagingInvocation, "", 60, 10); !errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) {
		t.Fatalf("old work released: %v", err)
	}
	stats, err := e.store.QueueStateForBinding(ctx, app.ID, replacement.ID)
	if err != nil || stats.Depth != 0 {
		t.Fatalf("replacement adopted backlog: %+v %v", stats, err)
	}
	tenant, _, err := e.store.(*state.PgStore).CreatePlatformTenant(ctx, e.acct.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	contextHeader, err := flags.EncodePropagationHeader(flags.PropagationContext{Version: flags.PropagationContextVersion, CustomerID: tenant.ID, Decisions: []flags.PropagationDecision{{Decision: flags.Decision{Flag: "export", Value: true, ConfigVersion: 7, Reason: "default", Source: "configuration"}, Origin: flags.EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()}}}})
	if err != nil {
		t.Fatal(err)
	}
	e.s.store = &recreatePGQueueEnvironmentOnAdmission{PgStore: e.store.(*state.PgStore), before: func() {
		if err := e.store.DeleteProjectEnvironment(ctx, e.acct.ID, project.ID, "staging"); err != nil {
			t.Fatal(err)
		}
		createEnvironment()
	}}
	rec = e.do(t, http.MethodPost, base+"/inbox", api.SendAppMessageRequest{Environment: "staging", Type: "order.created", Data: json.RawMessage(`{}`), FlagContext: contextHeader}, nil)
	assertProblem(t, rec, http.StatusConflict, "queue_binding_environment_unavailable")
	stats, err = e.store.QueueStateForBinding(ctx, app.ID, replacement.ID)
	if err != nil || stats.Depth != 0 {
		t.Fatalf("raced work admitted: %+v %v", stats, err)
	}
}
