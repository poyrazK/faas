package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type workflowRunsFixture struct {
	store          *state.MemStore
	caller, other  state.App
	workflow       state.App
	handler        http.Handler
	callerByRemote map[string]string
}

func newWorkflowRunsFixture(t *testing.T, plan api.Plan) workflowRunsFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "runs-"+uuid.NewString()+"@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	workflow := newApp("shop-orders")
	caller, other := newApp("shop-web"), newApp("shop-admin")
	manifest := caller.Manifest
	manifest.FunctionCallables = map[string]string{"orders": workflow.Slug}
	if caller, err = store.UpdateApp(ctx, caller.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: workflow.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:x",
		Workflows: json.RawMessage(`[{"name":"orders","trigger":{"type":"manual"},"steps":[],"code":{"path":"/"}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	f := workflowRunsFixture{store: store, caller: caller, other: other, workflow: workflow,
		callerByRemote: map[string]string{"10.0.0.1:1": caller.ID, "10.0.0.2:1": other.ID}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	f.handler = WithFunctionWorkflowRunAPI(next, func(_ context.Context, remote string) (string, string, error) {
		if id, ok := f.callerByRemote[remote]; ok {
			return id, "", nil
		}
		return "", "", errors.New("unknown")
	}, store)
	return f
}

func (f workflowRunsFixture) do(t *testing.T, remote, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+api.ScenarioPlatformAPIHost+":10081"+path, strings.NewReader(body))
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// ADR-20261113120824 §3: a function starts a workflow function it may call;
// only it reads or signals the run; retries with an Idempotency-Key replay.
func TestFunctionWorkflowRunAPI(t *testing.T) {
	f := newWorkflowRunsFixture(t, api.PlanPro)
	key := map[string]string{"Idempotency-Key": "order-1"}
	w := f.do(t, "10.0.0.1:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders","input":{"id":1}}`, key)
	var run api.FunctionWorkflowRunStatus
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &run) != nil || run.ID == "" || run.Status != state.WorkflowRunStatusPending || run.Done {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	stored, err := f.store.GetWorkflowRun(context.Background(), run.ID)
	if err != nil || stored.AppID != f.workflow.ID || stored.WorkflowName != "orders" || string(stored.Input) != `{"id":1}` {
		t.Fatalf("stored run = %+v, %v", stored, err)
	}
	if again := f.do(t, "10.0.0.1:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders","input":{"id":1}}`, key); again.Code != http.StatusCreated ||
		again.Header().Get("Idempotent-Replayed") != "true" || !strings.Contains(again.Body.String(), run.ID) {
		t.Fatalf("replay = %d %s", again.Code, again.Body)
	}

	if r := f.do(t, "10.0.0.1:1", http.MethodGet, "/v1/workflow-runs/"+run.ID, "", nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"pending"`) {
		t.Fatalf("read = %d %s", r.Code, r.Body)
	}
	if r := f.do(t, "10.0.0.1:1", http.MethodPost, "/v1/workflow-runs/"+run.ID+"/events", `{"event":"approved","payload":{"ok":true}}`, nil); r.Code != http.StatusAccepted {
		t.Fatalf("signal = %d %s", r.Code, r.Body)
	}
	events, _ := f.store.GetWorkflowEventsForRun(context.Background(), run.ID)
	if len(events) != 1 || events[0].EventName != "approved" {
		t.Fatalf("events = %+v", events)
	}

	for name, tc := range map[string]struct {
		remote, method, path, body string
		status                     int
	}{
		"another function reads":   {"10.0.0.2:1", http.MethodGet, "/v1/workflow-runs/" + run.ID, "", http.StatusNotFound},
		"another function signals": {"10.0.0.2:1", http.MethodPost, "/v1/workflow-runs/" + run.ID + "/events", `{"event":"x"}`, http.StatusNotFound},
		"not callable":             {"10.0.0.2:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders"}`, http.StatusBadRequest},
		"unknown caller":           {"10.9.9.9:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders"}`, http.StatusForbidden},
		"missing function":         {"10.0.0.1:1", http.MethodPost, "/v1/workflow-runs", `{}`, http.StatusBadRequest},
		"not a deployed workflow":  {"10.0.0.1:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders","workflow":"nope"}`, http.StatusNotFound},
		"unknown run":              {"10.0.0.1:1", http.MethodGet, "/v1/workflow-runs/" + uuid.NewString(), "", http.StatusNotFound},
	} {
		if r := f.do(t, tc.remote, tc.method, tc.path, tc.body, nil); r.Code != tc.status {
			t.Errorf("%s: %d %s, want %d", name, r.Code, r.Body, tc.status)
		}
	}
	other := httptest.NewRequest(http.MethodGet, "http://example.com/v1/workflow-runs", nil)
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, other)
	if w.Code != http.StatusTeapot {
		t.Fatalf("other host = %d, want pass-through", w.Code)
	}
}

// A plan without workflows refuses the start with the plan problem.
func TestFunctionWorkflowRunAPIPlanGate(t *testing.T) {
	f := newWorkflowRunsFixture(t, api.PlanFree)
	if w := f.do(t, "10.0.0.1:1", http.MethodPost, "/v1/workflow-runs", `{"function":"orders"}`, nil); w.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan = %d %s", w.Code, w.Body)
	}
}
