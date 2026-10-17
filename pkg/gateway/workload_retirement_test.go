// adr: 1331
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type retiredWorkloadTestBackend struct {
	Backend
	calls int
}

func (b *retiredWorkloadTestBackend) CheckWorkloadAdmission(context.Context, string, string) error {
	b.calls++
	return state.ErrEnvironmentWorkloadRetired
}

func TestRetiredWorkloadCannotServeACachedCustomerResponse(t *testing.T) {
	f := newSmokeCacheFixture(t)
	f.seed(t, "fresh")
	backend := &retiredWorkloadTestBackend{Backend: f.backend}
	f.handler.backend = backend
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, f.request(false))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "stable") {
		t.Fatalf("retired cache response: status=%d body=%q", w.Code, w.Body.String())
	}
	if backend.calls != 1 {
		t.Fatalf("scoped admission checks=%d", backend.calls)
	}
	outcome, entry := f.cache.Get(f.key)
	if outcome != "fresh" || string(entry.body) != "stable" {
		t.Fatal("retirement deleted retained cache data")
	}
	if f.candidateCalls.Load() != 0 {
		t.Fatal("retirement dispatched unqualified candidate")
	}
}
func TestRetiredWorkloadCannotUseParkedHeadOrEdgeAnswers(t *testing.T) {
	for _, request := range []struct{ method, path string }{{http.MethodHead, "/"}, {http.MethodGet, "/favicon.ico"}, {http.MethodGet, "/robots.txt"}} {
		t.Run(request.method+request.path, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			h.backend = &retiredWorkloadTestBackend{Backend: b}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(request.method, "http://jane-api.apps.dom"+request.path, nil))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("retired edge answer: %d", w.Code)
			}
		})
	}
}

type retirementAdmissionCacheStore struct {
	calls int
	read  func(string, string) (bool, error)
}

func (s *retirementAdmissionCacheStore) LiveDeployments(context.Context, string) ([]DeploymentWeightsRow, error) {
	return nil, nil
}
func (s *retirementAdmissionCacheStore) EnvironmentWorkloadAdmissionRetired(_ context.Context, app, scope string) (bool, error) {
	s.calls++
	return s.read(app, scope)
}

func TestWorkloadRetirementAdmissionCachePreservesKnownAvailability(t *testing.T) {
	unavailable := errors.New("database unavailable")
	blocked := false
	var storageErr error
	store := &retirementAdmissionCacheStore{read: func(string, string) (bool, error) { return blocked, storageErr }}
	backend := &PGBackend{store: store}
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); err != nil {
		t.Fatal(err)
	}
	storageErr = unavailable
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); err != nil || store.calls != 1 {
		t.Fatalf("known availability acquired a per-request storage dependency: calls=%d err=%v", store.calls, err)
	}
	backend.InvalidateWorkloadAdmission("app")
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); !errors.Is(err, unavailable) {
		t.Fatalf("invalidated admission failed open: %v", err)
	}
	storageErr = nil
	blocked = true
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); !errors.Is(err, state.ErrEnvironmentWorkloadRetired) {
		t.Fatalf("withdrawal did not close cached lane: %v", err)
	}
	blocked = false
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); !errors.Is(err, state.ErrEnvironmentWorkloadRetired) {
		t.Fatal("recovery opened without a new route generation")
	}
	backend.InvalidateWorkloadAdmission("app")
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); err != nil {
		t.Fatal("qualified recovery stayed cached closed", err)
	}
}
func TestWorkloadRetirementInvalidationRejectsAnOlderStorageRead(t *testing.T) {
	store := &retirementAdmissionCacheStore{}
	backend := &PGBackend{store: store}
	store.read = func(string, string) (bool, error) {
		if store.calls == 1 {
			backend.InvalidateWorkloadAdmission("app")
			return false, nil
		}
		return true, nil
	}
	if err := backend.CheckWorkloadAdmission(t.Context(), "app", "staging"); !errors.Is(err, state.ErrEnvironmentWorkloadRetired) || store.calls != 2 {
		t.Fatalf("old storage read repopulated admission after withdrawal: calls=%d err=%v", store.calls, err)
	}
}
