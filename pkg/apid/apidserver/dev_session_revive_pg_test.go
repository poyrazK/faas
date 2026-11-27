//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
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

// The revive reopens the tombstone before restoring it, because restore
// refuses a torn-down preview (ADR-20261030070000). A refused restore must
// put the tombstone back to torn_down, where the restore guard covers it.
func TestDevSessionReviveRefusedKeepsTombstoneTornDownPG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	path := "/v1/dev/sessions/gregale-api"
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
	// Past the restore window, RestoreApp refuses the tombstone.
	if _, err := (*e.Pool()).Exec(t.Context(), `UPDATE apps SET delete_grace_until = now() - interval '1 second' WHERE id = $1`, first.App.ID); err != nil {
		t.Fatal(err)
	}

	if again := e.Do(t, "PUT", path, api.UpsertDevSessionRequest{}, nil); again.Code != 409 {
		t.Fatalf("restart status = %d, want 409: %s", again.Code, again.Body.String())
	}
	tomb, err := (*e.Store()).AppBySlugIncludingDeleted(t.Context(), first.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if tomb.DeletedAt == nil || tomb.PreviewPrState != state.PreviewPrStateTornDown {
		t.Fatalf("tombstone deleted_at=%v state=%q, want deleted and %q", tomb.DeletedAt, tomb.PreviewPrState, state.PreviewPrStateTornDown)
	}
}

// adr: 1170 — a revived watch session gets at least the RAM a new one would
// (api.DevWatchRAMMB). A tombstone made at 512 MB before that default came
// back at 512 MB on every restart; a size someone raised is kept.
func TestDevSessionReviveRaisesWatchRAMPG(t *testing.T) {
	for _, tc := range []struct {
		name    string
		before  int
		watch   *api.DevWatch
		wantRAM int
	}{
		{"watch raises the old default", 512, &api.DevWatch{Command: "npm run dev"}, api.DevWatchDefaultRAMMB},
		{"watch keeps a larger size", 2048, &api.DevWatch{Command: "npm run dev"}, 2048},
		{"production-shaped keeps its size", 512, nil, 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanScale)
			path := "/v1/dev/sessions/gregale-web"
			created := e.Do(t, "PUT", path, api.UpsertDevSessionRequest{}, nil)
			if created.Code != 201 {
				t.Fatalf("create status = %d, want 201: %s", created.Code, created.Body.String())
			}
			var first api.DevSessionResponse
			if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
				t.Fatal(err)
			}
			before := tc.before
			if _, err := (*e.Store()).UpdateApp(t.Context(), first.App.ID, state.UpdateAppParams{RAMMB: &before}); err != nil {
				t.Fatal(err)
			}
			if stopped := e.Do(t, "DELETE", path, nil, nil); stopped.Code != 204 {
				t.Fatalf("destroy status = %d, want 204: %s", stopped.Code, stopped.Body.String())
			}
			again := e.Do(t, "PUT", path, api.UpsertDevSessionRequest{Watch: tc.watch}, nil)
			if again.Code != 200 {
				t.Fatalf("restart status = %d, want 200 (revived): %s", again.Code, again.Body.String())
			}
			app, err := (*e.Store()).AppByID(t.Context(), first.App.ID)
			if err != nil || app.DeletedAt != nil || app.RAMMB != tc.wantRAM {
				t.Fatalf("revived app RAM = %d deleted=%v (err %v), want %d", app.RAMMB, app.DeletedAt, err, tc.wantRAM)
			}
		})
	}
}
