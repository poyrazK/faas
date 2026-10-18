package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func decodeDevSession(t *testing.T, body []byte) api.DevSessionResponse {
	t.Helper()
	var out api.DevSessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode developer session: %v", err)
	}
	return out
}

// adr: 1170 — watch mode is stored per developer environment, reported on
// read, and cleared by an upsert that omits it.
func TestDevSessionWatchModeIsStoredReportedAndCleared(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	project := "watch-api"
	watch := &api.DevWatch{Command: "  npm run dev  "}
	created := e.Do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{Watch: watch}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	session := decodeDevSession(t, created.Body.Bytes())
	if session.Watch == nil || session.Watch.Command != "npm run dev" {
		t.Fatalf("created watch = %+v, want the trimmed command", session.Watch)
	}
	// The app's public hostname goes with the command, for the development
	// server's host check (api.DevWatchHostEnv).
	want := state.DevWatchSetting{Command: "npm run dev", Host: strings.TrimPrefix(session.App.URL, "https://")}
	if got, _ := (*e.Store()).DevWatch(t.Context(), session.App.ID); got != want || got.Host == "" {
		t.Fatalf("stored watch mode = %+v, want %+v", got, want)
	}
	read := e.Do(t, "GET", "/v1/dev/sessions/"+project, nil, nil)
	if got := decodeDevSession(t, read.Body.Bytes()).Watch; read.Code != http.StatusOK || got == nil || got.Command != "npm run dev" {
		t.Fatalf("read watch = %+v (status %d)", got, read.Code)
	}
	refreshed := e.Do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if refreshed.Code != http.StatusOK || decodeDevSession(t, refreshed.Body.Bytes()).Watch != nil {
		t.Fatalf("refresh without watch: status %d body %s", refreshed.Code, refreshed.Body.String())
	}
	if got, _ := (*e.Store()).DevWatch(t.Context(), session.App.ID); got != (state.DevWatchSetting{}) {
		t.Fatalf("watch mode after an upsert without watch = %+v, want cleared", got)
	}
}

func TestDevSessionWatchModeRefusals(t *testing.T) {
	t.Run("too little RAM", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanFree)
		resp := e.Do(t, "PUT", "/v1/dev/sessions/watch-small", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: "npm run dev"}}, nil)
		var problem api.Problem
		_ = json.Unmarshal(resp.Body.Bytes(), &problem)
		if resp.Code != http.StatusUnprocessableEntity || problem.Code != api.CodeDevWatchUnsupported || problem.Limit == nil || *problem.Limit != api.DevWatchMinRAMMB {
			t.Fatalf("status %d problem %+v, want 422 dev_watch_unsupported with the RAM limit", resp.Code, problem)
		}
		if _, err := (*e.Store()).AppBySlug(t.Context(), apidserver.DevSessionSlug((*e.Acct()).ID, "watch-small", "")); err == nil {
			t.Fatal("a refused watch request still created the developer environment")
		}
	})
	t.Run("invalid command", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanPro)
		for _, command := range []string{"", "   ", "npm run dev\nrm -rf /"} {
			resp := e.Do(t, "PUT", "/v1/dev/sessions/watch-bad", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: command}}, nil)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("command %q: status %d, want 400", command, resp.Code)
			}
		}
	})
	t.Run("unauthenticated environment", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanPro)
		created := e.Do(t, "PUT", "/v1/dev/sessions/watch-open", api.UpsertDevSessionRequest{}, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("create status = %d", created.Code)
		}
		app, err := (*e.Store()).AppBySlug(t.Context(), apidserver.DevSessionSlug((*e.Acct()).ID, "watch-open", ""))
		if err != nil {
			t.Fatal(err)
		}
		falsy := false
		opened := e.Do(t, "PATCH", "/v1/apps/"+app.Slug, api.UpdateAppRequest{RequireAuthn: &falsy,
			PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}, nil)
		if opened.Code != http.StatusOK {
			t.Fatalf("open app auth: status %d: %s", opened.Code, opened.Body.String())
		}
		resp := e.Do(t, "PUT", "/v1/dev/sessions/watch-open", api.UpsertDevSessionRequest{Watch: &api.DevWatch{Command: "npm run dev"}}, nil)
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422 for an open developer environment: %s", resp.Code, resp.Body.String())
		}
		if got, _ := (*e.Store()).DevWatch(t.Context(), app.ID); got != (state.DevWatchSetting{}) {
			t.Fatalf("refused request stored %+v", got)
		}
	})
}
