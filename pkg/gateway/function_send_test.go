package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionSendFixture struct {
	store   *state.MemStore
	account state.Account
	caller  state.App
	callee  state.App
}

func newFunctionSendFixture(t *testing.T, plan api.Plan) functionSendFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "send-"+uuid.NewString()+"@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Type: state.AppTypeFunction,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	return functionSendFixture{store: store, account: account, caller: newApp("shop-web"), callee: newApp("shop-users")}
}

// deploy makes both apps live in scope, publishes the scope's release, and
// returns each app's deployment by app id.
func (f functionSendFixture) deploy(t *testing.T, scope string) map[string]state.Deployment {
	t.Helper()
	ctx := t.Context()
	out := map[string]state.Deployment{}
	var members []state.ProjectReleaseMember
	for _, app := range []state.App{f.caller, f.callee} {
		dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		out[app.ID] = dep
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
	}
	if _, err := f.store.PublishProjectReleaseSet(ctx, f.account.ID, f.caller.ProjectID, scope, 1800, members); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f functionSendFixture) send(t *testing.T, callerDeployment string, header bool) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://shop-users.internal/", strings.NewReader(`{"email":"a@b"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.FunctionCallHeader, "1")
	req.Header.Set(api.ReleaseHeader, "rel_from_caller")
	if header {
		req.Header.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
	}
	w := httptest.NewRecorder()
	handled := NewFunctionSendAdmitter(f.store)(w, req,
		ServiceCaller{AppID: f.caller.ID, AccountID: f.account.ID, DeploymentID: callerDeployment},
		ServiceTarget{AppID: f.callee.ID}, "/users")
	return w, handled
}

// ADR-20261111150000 §3: send() over the binding is enqueued as a durable
// async invocation of the callee, in the caller's environment, and answered
// 202 with its id; a call without the marker is forwarded as before.
func TestFunctionSendEnqueuesInCallersEnvironment(t *testing.T) {
	f := newFunctionSendFixture(t, api.PlanPro)
	f.deploy(t, "production")
	staging := f.deploy(t, "staging")
	stagingCallee, stagingCaller := staging[f.callee.ID], staging[f.caller.ID]

	if w, handled := f.send(t, stagingCaller.ID, false); handled || w.Body.Len() != 0 {
		t.Fatalf("unmarked call was admitted: %d %s", w.Code, w.Body)
	}
	w, handled := f.send(t, stagingCaller.ID, true)
	if !handled || w.Code != http.StatusAccepted {
		t.Fatalf("send = %v %d %s", handled, w.Code, w.Body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.ID == "" || w.Header().Get(api.InvocationIDHeader) != accepted.ID {
		t.Fatalf("202 body %s: %v", w.Body, err)
	}
	inv, err := f.store.InvocationByID(t.Context(), accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inv.AppID != f.callee.ID || inv.Source != state.InvocationAsyncInvoke || inv.Path != "/users" || string(inv.Payload) != `{"email":"a@b"}` {
		t.Fatalf("invocation = %+v", inv)
	}
	if inv.DeploymentScope != "staging" || w.Header().Get(api.RevisionHeader) != stagingCallee.ID {
		t.Fatalf("send from staging enqueued in scope %q revision %q, want staging %q", inv.DeploymentScope, w.Header().Get(api.RevisionHeader), stagingCallee.ID)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	_, keptInvoke := headers[api.FunctionInvokeHeader]
	keptRelease := headers[api.ReleaseHeader] == "rel_from_caller"
	if keptInvoke || keptRelease || headers[api.FunctionCallHeader] != "1" {
		t.Fatalf("stored headers = %v: want the invoke marker and caller release dropped, the function-call marker kept", headers)
	}
}

func TestFunctionSendRefusals(t *testing.T) {
	t.Run("free plan", func(t *testing.T) {
		f := newFunctionSendFixture(t, api.PlanFree)
		f.deploy(t, "production")
		if w, handled := f.send(t, "", true); !handled || w.Code != http.StatusForbidden && w.Code != http.StatusPaymentRequired {
			t.Fatalf("free send = %v %d %s", handled, w.Code, w.Body)
		}
	})
	t.Run("rehearsal caller", func(t *testing.T) {
		f := newFunctionSendFixture(t, api.PlanPro)
		req := httptest.NewRequest(http.MethodPost, "http://shop-users.internal/", nil)
		req.Header.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
		w := httptest.NewRecorder()
		if !NewFunctionSendAdmitter(f.store)(w, req, ServiceCaller{RehearsalRunID: "run", AccountID: f.account.ID}, ServiceTarget{AppID: f.callee.ID}, "/") || w.Code != http.StatusForbidden {
			t.Fatalf("rehearsal send = %d %s", w.Code, w.Body)
		}
	})
	t.Run("revision override", func(t *testing.T) {
		f := newFunctionSendFixture(t, api.PlanPro)
		req := httptest.NewRequest(http.MethodPost, "http://shop-users.internal/", nil)
		req.Header.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
		req.Header.Set(api.TargetDeploymentHeader, "00000000-0000-0000-0000-000000000001")
		w := httptest.NewRecorder()
		if !NewFunctionSendAdmitter(f.store)(w, req, ServiceCaller{AccountID: f.account.ID}, ServiceTarget{AppID: f.callee.ID}, "/") || w.Code != http.StatusBadRequest {
			t.Fatalf("pinned send = %d %s", w.Code, w.Body)
		}
	})
}

func TestChainServiceProxyAsyncAdmittersFirstWins(t *testing.T) {
	var calls []string
	admitter := func(name string, handle bool) ServiceProxyAsyncAdmitter {
		return func(http.ResponseWriter, *http.Request, ServiceCaller, ServiceTarget, string) bool {
			calls = append(calls, name)
			return handle
		}
	}
	chain := ChainServiceProxyAsyncAdmitters(admitter("a", false), nil, admitter("b", true), admitter("c", true))
	if !chain(nil, nil, ServiceCaller{}, ServiceTarget{}, "") || strings.Join(calls, ",") != "a,b" {
		t.Fatalf("calls = %v", calls)
	}
}
