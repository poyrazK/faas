package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// ADR-20261026120412: a developer environment builds one command of a Go
// module, stores it as the app's Go command, echoes it, and drops it when a
// later request omits it.
func TestDevSessionGoCommand(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	project := "shop-worker"
	slug := apidserver.DevSessionSlug((*e.Acct()).ID, project, "")
	upsert := func(req api.UpsertDevSessionRequest, wantStatus int) api.DevSessionResponse {
		t.Helper()
		rec := e.Do(t, "PUT", "/v1/dev/sessions/"+project, req, nil)
		if rec.Code != wantStatus {
			t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body.String())
		}
		var out api.DevSessionResponse
		if wantStatus < 300 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	stored := func() string {
		t.Helper()
		app, err := (*e.Store()).AppBySlug(t.Context(), slug)
		if err != nil {
			t.Fatal(err)
		}
		return app.Manifest.BuildGoCommand
	}

	if got := upsert(api.UpsertDevSessionRequest{GoCommand: "worker"}, 201); got.GoCommand != "worker" || stored() != "worker" {
		t.Fatalf("created go command = %q, stored %q", got.GoCommand, stored())
	}
	if got := upsert(api.UpsertDevSessionRequest{GoCommand: "api"}, 200); got.GoCommand != "api" || stored() != "api" {
		t.Fatalf("refreshed go command = %q, stored %q", got.GoCommand, stored())
	}
	if got := upsert(api.UpsertDevSessionRequest{}, 200); got.GoCommand != "" || stored() != "" {
		t.Fatalf("omitted go command = %q, stored %q", got.GoCommand, stored())
	}

	upsert(api.UpsertDevSessionRequest{GoCommand: "../worker"}, 400)
	upsert(api.UpsertDevSessionRequest{GoCommand: "worker", Type: "function", Runtime: "node22"}, 400)
}
