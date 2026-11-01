// adr: 20261030125501 §3 — a stateless agent's instance issues callback URLs
// with a gregale:agents workload token, which binds only a live production
// instance of an active account's app.
package agents_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// workloadStore holds one account, its app, a live instance and the
// instance's deployment; each case breaks one of them.
type workloadStore struct {
	account    state.Account
	app        state.App
	instance   state.Instance
	deployment state.Deployment
}

func (s *workloadStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	if id != s.account.ID {
		return state.Account{}, state.ErrNotFound
	}
	return s.account, nil
}

func (s *workloadStore) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

func (s *workloadStore) AppByID(_ context.Context, id string) (state.App, error) {
	if id != s.app.ID {
		return state.App{}, state.ErrNotFound
	}
	return s.app, nil
}

func (s *workloadStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	if id != s.instance.ID {
		return state.Instance{}, state.ErrNotFound
	}
	return s.instance, nil
}

func (s *workloadStore) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	if id != s.deployment.ID {
		return state.Deployment{}, state.ErrNotFound
	}
	return s.deployment, nil
}

func TestWorkloadCaller(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://identity.example.test"
	signer, err := workloadidentity.NewSigner(key, issuer, "kid-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), issuer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	mint := func(t *testing.T, appID, instanceID, environment, audience string) string {
		t.Helper()
		tok, err := signer.MintEnvironment(now, callbackAccount, appID, instanceID, environment, audience)
		if err != nil {
			t.Fatal(err)
		}
		return tok.AccessToken
	}
	healthy := func() *workloadStore {
		return &workloadStore{
			account:    state.Account{ID: callbackAccount, Status: state.AccountActive},
			app:        state.App{ID: "app-1", AccountID: callbackAccount, Status: state.AppActive},
			instance:   state.Instance{ID: "inst-1", AppID: "app-1", DeploymentID: "dep-1", State: string(state.StateRunning)},
			deployment: state.Deployment{ID: "dep-1", AppID: "app-1"},
		}
	}
	good := mint(t, "app-1", "inst-1", workloadidentity.DefaultEnvironment, workloadidentity.AgentsAudience)
	for _, tc := range []struct {
		name   string
		token  string
		breaks func(*workloadStore)
		ok     bool
	}{
		{name: "live production instance", token: good, ok: true},
		{name: "queue audience", token: mint(t, "app-1", "inst-1", workloadidentity.DefaultEnvironment, workloadidentity.QueuesAudience)},
		{name: "staging token", token: mint(t, "app-1", "inst-1", "staging", workloadidentity.AgentsAudience)},
		{name: "not a token", token: "not-a-jwt"},
		{name: "app of another account", token: good, breaks: func(s *workloadStore) { s.app.AccountID = otherAccount }},
		{name: "deleted app", token: good, breaks: func(s *workloadStore) { s.app.Status = state.AppDeleted }},
		{name: "suspended account", token: good, breaks: func(s *workloadStore) { s.account.Status = state.AccountSuspended }},
		{name: "parked instance", token: good, breaks: func(s *workloadStore) { s.instance.State = string(state.StateParked) }},
		{name: "instance of another app", token: good, breaks: func(s *workloadStore) { s.instance.AppID = "app-2" }},
		{name: "staging deployment", token: good, breaks: func(s *workloadStore) { s.deployment.Scope = "staging" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := healthy()
			if tc.breaks != nil {
				tc.breaks(store)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/agent-callbacks", nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			caller, err := agents.WorkloadCaller(r, verifier, store, now)
			if !tc.ok {
				if err == nil {
					t.Fatalf("accepted as %+v", caller)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if caller.Account.ID != callbackAccount || caller.AppID != "app-1" {
				t.Fatalf("caller = %+v", caller)
			}
		})
	}
	if _, err := agents.WorkloadCaller(httptest.NewRequest(http.MethodPost, "/", nil), nil, healthy(), now); err == nil {
		t.Fatal("accepted without workload trust")
	}
}
