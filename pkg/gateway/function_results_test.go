package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionResultsFixture struct {
	store                      *state.MemStore
	account                    state.Account
	caller, callee, other, ext state.App
}

func newFunctionResultsFixture(t *testing.T) functionResultsFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "results-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string, callables map[string]string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeFunction,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{FunctionCallables: callables}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	f := functionResultsFixture{store: store, account: account}
	f.callee = newApp("shop-users", nil)
	f.other = newApp("shop-report", nil)
	f.caller = newApp("shop-web", map[string]string{"users": "shop-users", "report": "shop-report"})
	other, err := store.CreateAccount(ctx, "results-other-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.ext, err = store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "elsewhere", Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f functionResultsFixture) send(t *testing.T, onComplete string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://shop-users.internal/", strings.NewReader(`{"email":"a@b"}`))
	req.Header.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
	if onComplete != "" {
		req.Header.Set(api.FunctionOnCompleteHeader, onComplete)
	}
	w := httptest.NewRecorder()
	NewFunctionSendAdmitter(f.store)(w, req, ServiceCaller{AppID: f.caller.ID, AccountID: f.account.ID}, ServiceTarget{AppID: f.callee.ID}, "/")
	return w
}

func acceptedID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var accepted api.AsyncInvokeResponse
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &accepted) != nil || accepted.ID == "" {
		t.Fatalf("send = %d %s, want 202 with an id", w.Code, w.Body)
	}
	return accepted.ID
}

// ADR-20261112160000 §1 and §3: a send records its sender, and
// X-Gregale-On-Complete resolves to the caller itself or a name it may call;
// any other name is refused before anything is queued.
func TestFunctionSendRecordsSenderAndCompletion(t *testing.T) {
	f := newFunctionResultsFixture(t)
	for _, tc := range []struct {
		onComplete, want string
	}{
		{"", ""},
		{"self", f.caller.ID},
		{"report", f.other.ID},
	} {
		id := acceptedID(t, f.send(t, tc.onComplete))
		send, err := f.store.InvocationSend(t.Context(), id)
		if err != nil || send.SenderAppID != f.caller.ID || send.OnCompleteAppID != tc.want {
			t.Errorf("onComplete %q: send = %+v, %v; want sender %s and completion %q", tc.onComplete, send, err, f.caller.ID, tc.want)
		}
		inv, _ := f.store.InvocationByID(t.Context(), id)
		var headers map[string]string
		_ = json.Unmarshal(inv.Headers, &headers)
		if _, kept := headers[api.FunctionOnCompleteHeader]; kept {
			t.Errorf("onComplete %q: the completion marker was stored with the request: %v", tc.onComplete, headers)
		}
	}
	for _, name := range []string{"nobody", "elsewhere"} {
		if w := f.send(t, name); w.Code != http.StatusBadRequest {
			t.Errorf("onComplete %q = %d %s, want 400", name, w.Code, w.Body)
		}
	}
}

// ADR-20261112160000 §2: only the sender reads its invocation from its VM;
// ?wait holds the answer until the invocation is terminal.
func TestFunctionInvocationAPIReadsOnlyOwnSends(t *testing.T) {
	f := newFunctionResultsFixture(t)
	id := acceptedID(t, f.send(t, ""))
	callerIs := func(appID string) ServiceProxyCallerResolver {
		return func(context.Context, string) (string, error) { return appID, nil }
	}
	passed := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })
	get := func(appID, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		WithFunctionInvocationAPI(next, callerIs(appID), f.store).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w
	}
	read := func(w *httptest.ResponseRecorder) api.FunctionInvocationStatus {
		t.Helper()
		var status api.FunctionInvocationStatus
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil {
			t.Fatalf("read = %d %s", w.Code, w.Body)
		}
		return status
	}

	if status := read(get(f.caller.ID, "http://api.svc.gregale:10081/v1/invocations/"+id)); status.ID != id || status.Done {
		t.Fatalf("pending status = %+v", status)
	}
	if w := get(f.other.ID, "http://api.svc.gregale:10081/v1/invocations/"+id); w.Code != http.StatusNotFound {
		t.Fatalf("another app read the invocation: %d %s", w.Code, w.Body)
	}
	if w := get(f.caller.ID, "http://api.svc.gregale:10081/v1/invocations/"+uuid.NewString()); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", w.Code)
	}
	if get(f.caller.ID, "http://shop-users.internal/v1/invocations/"+id); !passed {
		t.Fatal("a request to another host was not passed through")
	}

	functionInvocationPoll = 10 * time.Millisecond
	t.Cleanup(func() { functionInvocationPoll = 250 * time.Millisecond })
	go func() {
		time.Sleep(50 * time.Millisecond)
		if _, err := f.store.ClaimInvocation(context.Background(), id, "i-1", 30); err == nil {
			_ = f.store.CompleteInvocation(context.Background(), id, json.RawMessage(`{"hello":"a@b"}`))
		}
	}()
	status := read(get(f.caller.ID, "http://api.svc.gregale:10081/v1/invocations/"+id+"?wait=5"))
	if !status.Done || status.State != string(state.InvocationCompleted) || string(status.Result) != `{"hello":"a@b"}` {
		t.Fatalf("waited status = %+v, want completed with the result", status)
	}
}
