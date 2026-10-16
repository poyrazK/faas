//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// H10-87: `gregale dev --stop` and the lease janitor soft-delete the
// session's app, and Postgres keeps a deleted app's slug reserved for the
// restore window (apps_slug_key covers tombstones). Starting the same session
// again failed with "developer session slug is already in use" for a week.
// It must revive the caller's own session instead. MemStore frees a deleted
// slug, so only Postgres reproduces this.
func TestDevSessionRestartAfterStopRevivesPG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	project := "gregale-api"
	path := "/v1/dev/sessions/" + project

	created := e.Do(t, "PUT", path, api.UpsertDevSessionRequest{}, nil)
	if created.Code != 201 {
		t.Fatalf("create status = %d, want 201: %s", created.Code, created.Body.String())
	}
	var first api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if stopped := e.Do(t, "DELETE", path, nil, nil); stopped.Code != 204 {
		t.Fatalf("destroy status = %d, want 204: %s", stopped.Code, stopped.Body.String())
	}

	again := e.Do(t, "PUT", path, api.UpsertDevSessionRequest{}, nil)
	if again.Code != 200 {
		t.Fatalf("restart status = %d, want 200 (revived): %s", again.Code, again.Body.String())
	}
	var second api.DevSessionResponse
	if err := json.Unmarshal(again.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.App.ID != first.App.ID || second.App.Slug != first.App.Slug {
		t.Fatalf("restart returned %s (%s), want the revived %s (%s)", second.App.ID, second.App.Slug, first.App.ID, first.App.Slug)
	}
	if until := time.Until(second.ExpiresAt); until < 23*time.Hour {
		t.Fatalf("revived lease ends in %s, want a fresh lease", until)
	}
	if _, err := (*e.Store()).AppBySlug(t.Context(), second.App.Slug); err != nil {
		t.Fatalf("revived session is not live: %v", err)
	}
}
