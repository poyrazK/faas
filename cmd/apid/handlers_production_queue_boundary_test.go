// adr: 568
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductionQueueAPIRejectsStageMessagesAndNotifications(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "queue-boundary", WorkloadName: "queue-boundary",
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, app, "stage", 0, []state.ProjectEnvironmentQueueDefinition{{Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2}}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, acct.ID, project.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	stage := func() state.Invocation {
		t.Helper()
		row, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, acct.ID, project.ID, dep.ID, "orders", state.Invocation{Payload: []byte(`{"stage_secret":true}`)})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	pending, dead, done := stage(), stage(), stage()
	for _, inv := range []state.Invocation{dead, done} {
		if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 300, 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FailInvocation(ctx, dead.ID, "private", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocation(ctx, done.ID, []byte(`{"stage_result":true}`)); err != nil {
		t.Fatal(err)
	}
	rows := []state.Invocation{pending, dead, done}
	for i := range rows {
		rows[i], err = store.InvocationByID(ctx, rows[i].ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, path, id, query string, handler accountHandler) *httptest.ResponseRecorder {
		t.Helper()
		r, w := projectRequest(method, "/v1/apps/"+app.Slug+path+query, app.Slug, []byte(`{}`))
		r.SetPathValue("id", id)
		handler(w, r, acct)
		return w
	}
	for _, inv := range rows {
		if w := call(http.MethodPost, "/queues/"+inv.ID+"/ack", inv.ID, "", srv.queueAck); w.Code != http.StatusNotFound {
			t.Fatalf("stage ack=%d %s", w.Code, w.Body.String())
		}
		if w := call(http.MethodPost, "/queues/dead_letter/"+inv.ID+"/replay", inv.ID, "", srv.queueDeadLetterReplay); w.Code != http.StatusNotFound {
			t.Fatalf("stage replay=%d %s", w.Code, w.Body.String())
		}
	}
	for _, item := range []struct {
		path    string
		handler accountHandler
	}{{"/queues/state", srv.queueState}, {"/queues/peek", srv.queuePeek}, {"/queues/dead_letter", srv.queueDeadLetter}} {
		w := call(http.MethodGet, item.path, "", "", item.handler)
		if w.Code != http.StatusOK || json.Valid(w.Body.Bytes()) == false {
			t.Fatalf("production read=%d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "stage_secret") || strings.Contains(w.Body.String(), "stage_result") {
			t.Fatal("production read exposed stage payload")
		}
		for _, inv := range rows {
			if strings.Contains(w.Body.String(), inv.ID) {
				t.Fatal("production read exposed stage ID")
			}
		}
		if w := call(http.MethodGet, item.path, "", "?environment=stage", item.handler); w.Code != http.StatusBadRequest {
			t.Fatalf("stage selector=%d %s", w.Code, w.Body.String())
		}
	}
	prod, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: acct.ID, Source: state.InvocationQueue, Payload: []byte(`{"production":true}`), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	payload := func(id string) string {
		b, _ := json.Marshal(map[string]string{"app_id": app.ID, "invocation_id": id})
		return string(b)
	}
	for _, forged := range []bool{true, false} {
		srv.notif = stubNotifier{hook: func(_ context.Context, channel string, predicate func(string) bool, _ time.Duration) (string, error) {
			if channel != db.NotifyInvocationDone || predicate(payload(done.ID)) || predicate(payload(pending.ID)) {
				t.Fatal("stage completion matched production receive")
			}
			if !predicate(payload(prod.ID)) {
				t.Fatal("production completion did not match receive")
			}
			if forged {
				return payload(done.ID), nil
			}
			return payload(prod.ID), nil
		}}
		w := call(http.MethodPost, "/queues/receive", "", "", srv.queueReceive)
		if forged {
			if w.Code != http.StatusNotFound {
				t.Fatalf("forged completion=%d %s", w.Code, w.Body.String())
			}
		} else {
			var response api.QueueReceiveResponse
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != http.StatusOK || response.ID != prod.ID {
				t.Fatalf("production receive=%d %s", w.Code, w.Body.String())
			}
		}
	}
	for _, before := range rows {
		after, err := store.InvocationByID(ctx, before.ID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("API changed stage: %+v %v", after, err)
		}
	}
	prodDead, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: acct.ID, Source: state.InvocationQueue, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocation(ctx, prodDead.ID, "", 300); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, prodDead.ID, "production", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	wrongApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "wrong-queue", WorkloadName: "wrong-queue"})
	if err != nil {
		t.Fatal(err)
	}
	r, w := projectRequest(http.MethodPost, "/v1/apps/"+wrongApp.Slug+"/queues/dead_letter/"+prodDead.ID+"/replay", wrongApp.Slug, nil)
	r.SetPathValue("id", prodDead.ID)
	srv.queueDeadLetterReplay(w, r, acct)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-app replay=%d %s", w.Code, w.Body.String())
	}
	// Selection validation runs outside the method/path idempotency namespace.
	for _, item := range []struct {
		name, path, body, id string
		handler              accountHandler
		status               int
	}{
		{"send", "/queues/send", `{"payload":{}}`, "", srv.queueSend, http.StatusCreated},
		{"inbox", "/inbox", `{"type":"order.created","data":{}}`, "", srv.sendAppMessage, http.StatusAccepted},
		{"ack", "/queues/" + prod.ID + "/ack", `{}`, prod.ID, srv.queueAck, http.StatusNoContent},
		{"replay", "/queues/dead_letter/" + prodDead.ID + "/replay", `{}`, prodDead.ID, srv.queueDeadLetterReplay, http.StatusAccepted},
	} {
		handler := productionQueueBindingHandler(srv.idempotent(item.handler))
		request := func(query string) *httptest.ResponseRecorder {
			r, w := projectRequest(http.MethodPost, "/v1/apps/"+app.Slug+item.path+query, app.Slug, []byte(item.body))
			r.SetPathValue("id", item.id)
			r.Header.Set("Idempotency-Key", "boundary-"+item.name)
			handler(w, r, acct)
			return w
		}
		assertProblem(t, request("?environment=stage"), http.StatusBadRequest, api.CodeValidation)
		w := request("")
		if w.Code != item.status {
			t.Fatalf("production %s=%d %s", item.name, w.Code, w.Body.String())
		}
		assertProblem(t, request("?environment=stage"), http.StatusBadRequest, api.CodeValidation)
		if replay := request("?environment=production"); replay.Code != item.status || replay.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("receipt %s=%d %s", item.name, replay.Code, replay.Body.String())
		}
	}
}

func TestProductionDeadLetterAPINeverDiscardsStageSelection(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "boundary-dlq")
	seedDeadLetterRow(t, e, appID, "production")
	for _, prefix := range []string{"/v1/apps/boundary-dlq/dlq", "/v1/account/dlq"} {
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, prefix}, {http.MethodGet, prefix + "/unknown"},
			{http.MethodPost, prefix + ":replay_all"}, {http.MethodPost, prefix + "/unknown/replay"},
			{http.MethodDelete, prefix}, {http.MethodDelete, prefix + "/unknown"},
		} {
			for _, query := range []string{"?environment=stage", "?environment=", "?environment=production&environment=stage", "?scope=stage"} {
				assertProblem(t, e.do(t, route.method, route.path+query, nil, map[string]string{"Idempotency-Key": "stage-dlq-selector"}), http.StatusBadRequest, api.CodeValidation)
			}
		}
		path := prefix + ":replay_all"
		headers := map[string]string{"Idempotency-Key": "production-dlq-receipt"}
		first := e.do(t, http.MethodPost, path, nil, headers)
		if first.Code != http.StatusAccepted {
			t.Fatalf("production bulk replay=%d %s", first.Code, first.Body.String())
		}
		assertProblem(t, e.do(t, http.MethodPost, path+"?environment=stage", nil, headers), http.StatusBadRequest, api.CodeValidation)
		if replay := e.do(t, http.MethodPost, path+"?environment=production", nil, headers); replay.Code != first.Code || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != first.Body.String() {
			t.Fatalf("production bulk receipt=%d %s", replay.Code, replay.Body.String())
		}
	}
}
