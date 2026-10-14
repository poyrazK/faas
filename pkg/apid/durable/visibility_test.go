package durable

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// slugHost resolves every slug to one app so App can be exercised without apid.
type slugHost struct {
	fakeHost
	app state.App
}

func (h slugHost) LoadApp(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool) {
	return h.app, true
}

func assertNoSuchRoute(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var problem struct{ Code, Detail string }
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "not_found" || problem.Detail != "no such API route" {
		t.Fatalf("problem = %+v, want the unregistered-route 404", problem)
	}
}

func TestRouteHidesEntityRoutesWhilePreviewIsOff(t *testing.T) {
	called := false
	next := func(http.ResponseWriter, *http.Request) { called = true }

	rec := httptest.NewRecorder()
	(&Service{}).Route(next)(rec, httptest.NewRequest(http.MethodPost, "/v1/apps/a/entities/invoke", nil))
	assertNoSuchRoute(t, rec)
	if called {
		t.Fatal("hidden route reached its handler")
	}

	enabled := &Service{Engine: &durableentity.Manager{}}
	enabled.Route(next)(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/apps/a/entities/invoke", nil))
	if !called {
		t.Fatal("enabled preview did not reach the handler")
	}
}

func TestAppHidesEntityRoutesFromAppsOutsideTheAllowlist(t *testing.T) {
	app := state.App{ID: "11111111-1111-4111-8111-111111111111"}
	svc := &Service{Engine: &durableentity.Manager{}, Host: slugHost{app: app}, Apps: map[string]bool{}}
	called := false
	handler := svc.App(func(http.ResponseWriter, *http.Request, state.Account) { called = true })

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/v1/apps/a/entities/inspect", nil), state.Account{})
	assertNoSuchRoute(t, rec)
	if called {
		t.Fatal("non-allowlisted app reached the handler")
	}

	svc.Apps[app.ID] = true
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/apps/a/entities/inspect", nil), state.Account{})
	if !called {
		t.Fatal("allowlisted app did not reach the handler")
	}
}

func TestHiddenMatchesOnlyEntityPathsWhilePreviewIsOff(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/apps/a/entities":             true,
		"/v1/apps/a/entities/":            true,
		"/v1/apps/a/entities/invoke":      true,
		"/v1/apps/a/entities/backups/get": true,
		"/v1/apps/a/entitiesx":            false,
		"/v1/apps/a/invoke":               false,
		"/v1/apps//entities":              false,
		"/v1/apps/entities":               false,
		"/v1/entities":                    false,
	} {
		if got := (&Service{}).Hidden(httptest.NewRequest(http.MethodGet, path, nil)); got != want {
			t.Errorf("Hidden(%q) = %v, want %v", path, got, want)
		}
	}
	enabled := &Service{Engine: &durableentity.Manager{}}
	if enabled.Hidden(httptest.NewRequest(http.MethodGet, "/v1/apps/a/entities/invoke", nil)) {
		t.Error("enabled preview hid an entity route")
	}
}
