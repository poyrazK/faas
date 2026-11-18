package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grace"
)

// The Compose teardown-and-redeploy report: an app deleted with
// `gregale app delete` held its slug for the seven-day restore window, so the
// same Compose file could not deploy again. The owner's purge frees the slug
// at once: delete, purge, and the same name creates again.
func TestPurgeDeletedAppFreesTheSlugForTheOwner(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.purgeDeletedApp = grace.New(grace.Params{
		Store: e.store, Registry: prometheus.NewRegistry(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).PurgeApp
	id := mustSeedApp(t, e, "compose-web")
	if r := e.do(t, http.MethodDelete, "/v1/apps/compose-web", nil, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", r.Code, r.Body)
	}
	if r := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "compose-web"}, nil); r.Code != http.StatusConflict {
		t.Fatalf("create inside the window = %d, want 409: %s", r.Code, r.Body)
	}
	if r := e.do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/purge", map[string]any{"dry_run": true}, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("purge with an unknown intent = %d, want 400", r.Code)
	}
	r := e.do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/purge", nil, nil)
	var out api.DeletedAppPurgeResponse
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || r.Code != http.StatusOK || !out.Purged || out.Slug != "compose-web" {
		t.Fatalf("purge = %d %+v %v: %s", r.Code, out, err, r.Body)
	}
	if _, err := e.store.AppByID(context.Background(), id); err == nil {
		t.Fatal("purged app still present")
	}
	if r := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "compose-web"}, nil); r.Code != http.StatusCreated && r.Code != http.StatusOK {
		t.Fatalf("recreate after purge = %d: %s", r.Code, r.Body)
	}
}

// Without the in-process sweep the purge still closes the window, answers
// 202, and leaves the removal to the sweep; a read-only key cannot purge.
func TestPurgeDeletedAppWithoutSweepIsPendingAndNeedsDeployWrite(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pending-purge")
	if _, err := e.store.ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if r := e.doAs(t, http.MethodPost, "/v1/deleted-apps/"+id+"/purge", nil, nil, pt); r.Code != http.StatusForbidden {
		t.Fatalf("read-only purge = %d: %s", r.Code, r.Body)
	}
	r := e.do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/purge", nil, nil)
	var out api.DeletedAppPurgeResponse
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || r.Code != http.StatusAccepted || out.Purged || out.PendingReason == "" {
		t.Fatalf("purge = %d %+v %v: %s", r.Code, out, err, r.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil || app.DeleteGraceUntil == nil || app.DeleteGraceUntil.After(time.Now()) {
		t.Fatalf("window after a pending purge = %v %v, want closed", app.DeleteGraceUntil, err)
	}
}
