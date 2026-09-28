package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type unavailableSafeDeployDB struct{ state.Store }

func (unavailableSafeDeployDB) Ping(context.Context) error {
	return errors.New("database unavailable")
}

func TestInternalSafeDeployProbeRejectsUnavailableDatabase(t *testing.T) {
	const canaryToken = "canary-service-secret-0000000000000001"
	const actionToken = "action-service-secret-0000000000000001"
	s := &server{store: unavailableSafeDeployDB{Store: state.NewMemStore()}}
	mux := http.NewServeMux()
	if err := s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, token string }{
		{"/v1/internal/safe-deploy/canary/readyz", canaryToken},
		{"/v1/internal/safe-deploy/action/readyz", actionToken},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s with database down: status=%d, want 503", tc.path, rec.Code)
		}
	}
	operator := httptest.NewServer(mux)
	defer operator.Close()
	if err := api.NewInternalSafeDeployClient(operator.URL, canaryToken, actionToken).ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("database outage passed operator probe")
	}
}

func TestInternalSafeDeployProbesRequireEachCredentialAndLoopback(t *testing.T) {
	e := setup(t, api.PlanPro)
	const canaryToken = "canary-service-secret-0000000000000001"
	const actionToken = "action-service-secret-0000000000000001"
	const canaryPath = "/v1/internal/safe-deploy/canary/readyz"
	const actionPath = "/v1/internal/safe-deploy/action/readyz"
	for _, path := range []string{canaryPath, actionPath} {
		if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("public route %s returned %d, want 404", path, rec.Code)
		}
	}
	mux := http.NewServeMux()
	disabledMux := http.NewServeMux()
	if err := e.s.mountInternalSafeDeploy(disabledMux, "127.0.0.1:9101", "", ""); err != nil {
		t.Fatal(err)
	}
	disabledRec := httptest.NewRecorder()
	disabledMux.ServeHTTP(disabledRec, httptest.NewRequest(http.MethodGet, canaryPath, nil))
	if disabledRec.Code != http.StatusNotFound {
		t.Fatalf("disabled operator probe status=%d, want 404", disabledRec.Code)
	}
	if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, token, remote string
		want                int
	}{
		{canaryPath, canaryToken, "127.0.0.1:1234", http.StatusNoContent},
		{actionPath, actionToken, "127.0.0.1:1234", http.StatusNoContent},
		{canaryPath, actionToken, "127.0.0.1:1234", http.StatusUnauthorized},
		{actionPath, canaryToken, "127.0.0.1:1234", http.StatusUnauthorized},
		{canaryPath, canaryToken, "192.0.2.1:1234", http.StatusForbidden},
		{actionPath, actionToken, "192.0.2.1:1234", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = tc.remote
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("GET %s from %s: status=%d, want %d", tc.path, tc.remote, rec.Code, tc.want)
		}
		if rec.Code == http.StatusNoContent && (rec.Body.Len() != 0 || rec.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("GET %s: body=%q cache-control=%q", tc.path, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	}
	operator := httptest.NewServer(mux)
	client := api.NewInternalSafeDeployClient(operator.URL, canaryToken, actionToken)
	if err := client.ProbeSafeRelease(t.Context()); err != nil {
		t.Fatalf("healthy operator probes: %v", err)
	}
	if err := api.NewInternalSafeDeployClient(operator.URL, "canary-service-secret-0000000000000002", actionToken).ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("wrong canary credential passed probe")
	}
	if err := api.NewInternalSafeDeployClient(operator.URL, canaryToken, "action-service-secret-0000000000000002").ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("wrong recovery credential passed probe")
	}
	wrongService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := api.NewInternalSafeDeployClient(wrongService.URL, canaryToken, actionToken).ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("generic HTTP 200 passed operator probe")
	}
	wrongService.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == canaryPath {
			http.Redirect(w, r, "/healthy", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if err := api.NewInternalSafeDeployClient(redirector.URL, canaryToken, actionToken).ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("redirected operator probe passed")
	}
	redirector.Close()
	operator.Close()
	if err := client.ProbeSafeRelease(t.Context()); err == nil {
		t.Fatal("unavailable operator listener passed probe")
	}
	restarted := httptest.NewServer(mux)
	defer restarted.Close()
	if err := api.NewInternalSafeDeployClient(restarted.URL, canaryToken, actionToken).ProbeSafeRelease(t.Context()); err != nil {
		t.Fatalf("restarted operator probes: %v", err)
	}
}
