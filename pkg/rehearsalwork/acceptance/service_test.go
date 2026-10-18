package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *fixture) privateWorkerProxy(t *testing.T) *gateway.ServiceProxy {
	t.Helper()
	a := f.apps["worker"]
	_, e := f.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AppID: a.ID, AccountID: f.account.ID, MatchHost: a.Slug + ".example.invalid", MatchPath: "/import", MatchMethods: []string{"POST"}, Enabled: true, Kind: state.EdgeRuleKindAsync, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindAsync, Async: &state.EdgeRuleAsyncAction{RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 3, BaseSeconds: 1, MaxSeconds: 2}}}})
	if e != nil {
		t.Fatal(e)
	}
	return gateway.NewServiceProxy(gateway.ServiceProxyConfig{
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			id := f.identities["primary"]
			return id.AppID, id.DeploymentID, nil
		},
		Resolve: func(_ context.Context, caller, name string) (gateway.ServiceTarget, bool, error) {
			return gateway.ServiceTarget{AppID: a.ID, ScenarioTestRunID: f.run}, caller == f.apps["primary"].ID && name == "worker", nil
		},
		Authorize: func(_ context.Context, caller, target string) (gateway.ServiceCaller, error) {
			if caller != f.apps["primary"].ID || target != a.ID {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
			}
			return gateway.ServiceCaller{AppID: caller, AccountID: f.account.ID, RehearsalRunID: f.run}, nil
		},
		AdmitManagedAsync: gateway.NewRehearsalAsyncAdmitter(f.store),
	})
}

func (f *fixture) triggerImport(t *testing.T) (string, state.Invocation) {
	t.Helper()
	proxy := f.privateWorkerProxy(t)
	r := httptest.NewRequest("POST", "http://worker.svc.gregale:10081/import", strings.NewReader(`{"customer":9007199254740993}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "must-not-persist")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("private async admission: %d %s", w.Code, w.Body.String())
	}
	var accepted api.AsyncInvokeResponse
	if e := json.Unmarshal(w.Body.Bytes(), &accepted); e != nil {
		t.Fatal(e)
	}
	j, e := f.store.InvocationByID(t.Context(), accepted.ID)
	if e != nil || j.State != state.InvocationPending || j.RetryPolicyJSON == nil {
		t.Fatalf("private call was not durable async work: %+v %v", j, e)
	}
	if strings.Contains(string(j.Headers), "must-not-persist") {
		t.Fatal("private async admission persisted caller credentials")
	}
	return `{"accepted":true}`, j
}

func TestManagedWorkerPrivateRoutesHonorAdmissionAndScope(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFixture(t, backend, false)
			proxy := f.privateWorkerProxy(t)
			request := func(body string, headers map[string]string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "http://worker.svc.gregale:10081/import", strings.NewReader(body))
				for k, v := range headers {
					r.Header.Set(k, v)
				}
				w := httptest.NewRecorder()
				proxy.ServeHTTP(w, r)
				return w
			}
			if w := request(`{}`, map[string]string{gateway.ServiceProxyCallerAppHeader: f.apps["unbound"].ID}); w.Code != 403 {
				t.Fatal("forged caller reached async admission")
			}
			if w := request(strings.Repeat("x", 8193), nil); w.Code < 400 {
				t.Fatal("oversized async work was admitted")
			}
			if w := request(`{}`, map[string]string{api.ReleaseHeader: "requested-release"}); w.Code < 400 {
				t.Fatal("release override reached async admission")
			}
			w := request(`{}`, nil)
			if w.Code != 202 {
				t.Fatalf("admission: %d %s", w.Code, w.Body.String())
			}
			_ = f.seal(t)
			if w = request(`{}`, nil); w.Code != 409 {
				t.Fatalf("closing private request was not refused: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
