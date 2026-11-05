package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSharedAppLogsRequireCurrentOrganizationAccess(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "archive"}[archive], func(t *testing.T) {
			store := state.NewMemStore()
			owner, err := store.CreateAccount(t.Context(), "logs-owner@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			viewer, err := store.CreateAccount(t.Context(), "logs-viewer@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			org, err := store.CreateOrg(t.Context(), state.Org{Slug: "logs-team", Name: "Logs Team", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range []struct {
				id   string
				role state.OrgRole
			}{{owner.ID, state.OrgRoleOwner}, {viewer.ID, state.OrgRoleViewer}} {
				if err := store.AddOrgMember(t.Context(), org.ID, m.id, m.role, nil); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, OrgID: org.ID, Slug: "team-api"}); err != nil {
				t.Fatal(err)
			}
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, viewer.ID, hash, "viewer", []string{api.ScopeAppsRead}, nil); err != nil {
				t.Fatal(err)
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			auth := authmw.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
			mux := http.NewServeMux()
			path := "/v1/apps/team-api/logs?follow=0"
			want := http.StatusOK
			if archive {
				mux.Handle("GET /v1/apps/{slug}/logs", &ArchiveLogsHandler{Auth: auth, Store: store, Log: log})
				path += "&archive=1"
				want = http.StatusServiceUnavailable // authorization passes; no archive provider in this fixture
			} else {
				stream := newControllableScheddStream()
				stream.closeStream()
				mux.Handle("GET /v1/apps/{slug}/logs", &AppLogsHandler{Auth: auth, Store: store, Log: log, ScheddFor: &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}})
			}
			read := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("Authorization", "Bearer "+plain)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			if w := read(); w.Code != want {
				t.Fatalf("viewer: status=%d body=%s; want %d", w.Code, w.Body, want)
			}
			if err := store.RemoveOrgMember(t.Context(), org.ID, viewer.ID); err != nil {
				t.Fatal(err)
			}
			if w := read(); w.Code != 401 {
				t.Fatalf("removed viewer: status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

// The logs kill switch (ADR-20261023115741): while logs is off for the
// caller's account, GET /v1/apps/{slug}/logs (live and archive) answers
// exactly as an unknown route, whether or not the app exists.
func TestAppLogsHiddenWhileLogsCapabilityOff(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "archive"}[archive], func(t *testing.T) {
			store := state.NewMemStore()
			acct, err := store.CreateAccount(t.Context(), "logs-off@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "my-api"}); err != nil {
				t.Fatal(err)
			}
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "test", []string{api.ScopeAppsRead}); err != nil {
				t.Fatal(err)
			}
			released := true
			allowed := func(capability, accountID string) bool {
				if capability != capabilityLogs || accountID != acct.ID {
					t.Errorf("decided %q for %q, want %q for the caller", capability, accountID, capabilityLogs)
				}
				return released
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			auth := authmw.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
			mux := http.NewServeMux()
			query := "?follow=0"
			want := http.StatusOK
			if archive {
				mux.Handle("GET /v1/apps/{slug}/logs", &ArchiveLogsHandler{Auth: auth, Store: store, Log: log, CapabilityAllowed: allowed})
				query += "&archive=1"
				want = http.StatusServiceUnavailable // released; no archive provider in this fixture
			} else {
				stream := newControllableScheddStream()
				stream.closeStream()
				mux.Handle("GET /v1/apps/{slug}/logs", &AppLogsHandler{Auth: auth, Store: store, Log: log, CapabilityAllowed: allowed, ScheddFor: &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}})
			}
			read := func(slug string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/v1/apps/"+slug+"/logs"+query, nil)
				r.Header.Set("Authorization", "Bearer "+plain)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			if w := read("my-api"); w.Code != want {
				t.Fatalf("released: status=%d body=%s; want %d", w.Code, w.Body, want)
			}

			released = false
			hidden := read("my-api")
			if hidden.Code != http.StatusNotFound || !strings.Contains(hidden.Body.String(), "no such API route") {
				t.Fatalf("off: status=%d body=%s; want the unknown-route 404", hidden.Code, hidden.Body)
			}
			if missing := read("no-such-app"); missing.Body.String() != hidden.Body.String() {
				t.Fatalf("off: a missing app answers %s, an existing one %s; the route must not reveal which apps exist", missing.Body, hidden.Body)
			}
		})
	}
}
