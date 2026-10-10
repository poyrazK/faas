package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func decodeDevSession(t *testing.T, body []byte) api.DevSessionResponse {
	t.Helper()
	var out api.DevSessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode developer session: %v", err)
	}
	return out
}

// adr: 970 — watch mode is stored per developer environment, reported on
// read, and cleared by an upsert that omits it.
func TestDevSessionWatchModeIsStoredReportedAndCleared(t *testing.T) {
	e := setup(t, api.PlanPro)
	project := "watch-api"
	watch := &api.DevWatch{Command: "  npm run dev  "}
	created := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{Watch: watch}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	session := decodeDevSession(t, created.Body.Bytes())
	if session.Watch == nil || session.Watch.Command != "npm run dev" {
		t.Fatalf("created watch = %+v, want the trimmed command", session.Watch)
	}
	if got, _ := e.store.DevWatchCommand(t.Context(), session.App.ID); got != "npm run dev" {
		t.Fatalf("stored command = %q", got)
	}
	read := e.do(t, "GET", "/v1/dev/sessions/"+project, nil, nil)
	if got := decodeDevSession(t, read.Body.Bytes()).Watch; read.Code != http.StatusOK || got == nil || got.Command != "npm run dev" {
		t.Fatalf("read watch = %+v (status %d)", got, read.Code)
	}
	refreshed := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if refreshed.Code != http.StatusOK || decodeDevSession(t, refreshed.Body.Bytes()).Watch != nil {
		t.Fatalf("refresh without watch: status %d body %s", refreshed.Code, refreshed.Body.String())
	}
	if got, _ := e.store.DevWatchCommand(t.Context(), session.App.ID); got != "" {
		t.Fatalf("command after an upsert without watch = %q, want cleared", got)
	}
}

func TestDevSessionWatchModeRefusals(t *testing.T) {
	t.Run("too little RAM", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		resp := e.do(t, "PUT", "/v1/dev/sessions/watch-small", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: "npm run dev"}}, nil)
		var problem api.Problem
		_ = json.Unmarshal(resp.Body.Bytes(), &problem)
		if resp.Code != http.StatusUnprocessableEntity || problem.Code != api.CodeDevWatchUnsupported || problem.Limit == nil || *problem.Limit != api.DevWatchMinRAMMB {
			t.Fatalf("status %d problem %+v, want 422 dev_watch_unsupported with the RAM limit", resp.Code, problem)
		}
		if _, err := e.store.AppBySlug(t.Context(), devSessionSlug(e.acct.ID, "watch-small", "")); err == nil {
			t.Fatal("a refused watch request still created the developer environment")
		}
	})
	t.Run("invalid command", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		for _, command := range []string{"", "   ", "npm run dev\nrm -rf /"} {
			resp := e.do(t, "PUT", "/v1/dev/sessions/watch-bad", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: command}}, nil)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("command %q: status %d, want 400", command, resp.Code)
			}
		}
	})
	t.Run("unauthenticated environment", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		created := e.do(t, "PUT", "/v1/dev/sessions/watch-open", api.UpsertDevSessionRequest{}, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("create status = %d", created.Code)
		}
		app, err := e.store.AppBySlug(t.Context(), devSessionSlug(e.acct.ID, "watch-open", ""))
		if err != nil {
			t.Fatal(err)
		}
		falsy := false
		opened := e.do(t, "PATCH", "/v1/apps/"+app.Slug, api.UpdateAppRequest{RequireAuthn: &falsy,
			PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}, nil)
		if opened.Code != http.StatusOK {
			t.Fatalf("open app auth: status %d: %s", opened.Code, opened.Body.String())
		}
		resp := e.do(t, "PUT", "/v1/dev/sessions/watch-open", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: "npm run dev"}}, nil)
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422 for an open developer environment: %s", resp.Code, resp.Body.String())
		}
		if got, _ := e.store.DevWatchCommand(t.Context(), app.ID); got != "" {
			t.Fatalf("refused request stored %q", got)
		}
	})
}
