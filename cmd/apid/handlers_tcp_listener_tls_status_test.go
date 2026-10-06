package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type failingTLSObservationStore struct {
	*state.MemStore
	reads int
}

func (s *failingTLSObservationStore) ListTCPListenerTLSObservations(context.Context, string) ([]state.TCPListenerTLSObservation, error) {
	s.reads++
	return nil, errors.New("database credential=private-test-secret")
}

func TestTCPListenerTLSStatusStorageFailureAndOwnership(t *testing.T) {
	_, _, memory, owner := buildTestServer(t)
	app, err := memory.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "tls-status-failure", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.CreateTCPListener(t.Context(), state.TCPListener{AccountID: owner.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: 40150}); err != nil {
		t.Fatal(err)
	}
	store := &failingTLSObservationStore{MemStore: memory}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	request := func(account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("name", "echo")
		w := httptest.NewRecorder()
		srv.appTCPListenerTLSStatus(w, r, account)
		return w
	}
	foreign := state.Account{ID: "foreign-account"}
	if response := request(foreign); response.Code != http.StatusNotFound || store.reads != 0 {
		t.Fatalf("foreign account reached observations: status=%d reads=%d", response.Code, store.reads)
	}
	response := request(owner)
	if response.Code != http.StatusServiceUnavailable || store.reads != 1 {
		t.Fatalf("storage failure response: status=%d reads=%d body=%s", response.Code, store.reads, response.Body)
	}
	if strings.Contains(response.Body.String(), "private-test-secret") || strings.Contains(response.Body.String(), "credential") {
		t.Fatalf("observation failure exposed storage details: %s", response.Body)
	}
}

func TestTCPListenerTLSStatusAPI(t *testing.T) {
	handler, key, store, account := buildTestServer(t)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "tls-status", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := store.CreateTCPListener(t.Context(), state.TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: 40149, Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/tls-status/tcp-listeners/echo/tls-status"
	request := func(path, token string, want int) api.TCPListenerTLSStatusResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		var out api.TCPListenerTLSStatusResponse
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	request(path, "", http.StatusUnauthorized)
	out := request(path, key, http.StatusOK)
	if out.Scope != "observed_edges" || out.Name != "echo" || out.Observations == nil || len(out.Observations) != 0 {
		t.Fatalf("missing evidence projection=%+v", out)
	}
	observation := state.TCPListenerTLSObservation{ListenerID: listener.ID, EdgeID: "edge-one", Hostname: listener.TLSHostname, IntentUpdatedAt: listener.UpdatedAt, ObservedAt: time.Now(), Ready: true, NotAfter: time.Now().Add(time.Hour)}
	if err := store.PutTCPListenerTLSObservation(t.Context(), observation); err != nil {
		t.Fatal(err)
	}
	out = request(path, key, http.StatusOK)
	if len(out.Observations) != 1 || out.Observations[0].Status != "ready" || out.Observations[0].NotAfter == nil {
		t.Fatalf("ready projection=%+v", out)
	}
	if _, err := store.SetTCPListenerEnabled(t.Context(), listener.ID, false); err != nil {
		t.Fatal(err)
	}
	out = request(path, key, http.StatusOK)
	if out.Enabled || out.Observations[0].Status != "unknown" || out.Observations[0].NotAfter != nil {
		t.Fatalf("disabled evidence claimed readiness: %+v", out)
	}
	other, err := store.CreateAccount(t.Context(), "tls-status-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "tls-status-other", RAMMB: 256, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	request("/v1/apps/tls-status-other/tcp-listeners/echo/tls-status", key, http.StatusNotFound)
	request("/v1/apps/tls-status/tcp-listeners/missing/tls-status", key, http.StatusNotFound)
	stale := tcpListenerTLSStatusResponse(listener, []state.TCPListenerTLSObservation{observation}, observation.ObservedAt.Add(api.TCPListenerTLSObservationMaxAge))
	if stale.Observations[0].Status != "unknown" || stale.Observations[0].NotAfter != nil {
		t.Fatalf("stale evidence exposed expiry as current: %+v", stale)
	}
}
