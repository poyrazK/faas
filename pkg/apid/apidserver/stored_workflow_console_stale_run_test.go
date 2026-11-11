package apidserver_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoredWorkflowConsoleRejectsRunFromStalePage(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/workflows/stored"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "stale-run")
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != http.StatusOK {
		t.Fatalf("save: %d %s", page.Code, page.Body.String())
	}
	draft := consoleVersion(t, e, app.Slug, "stale-run")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", page.Code, page.Body.String())
	}
	published := consoleVersion(t, e, app.Slug, "stale-run")
	form.Set("expected_version", strconv.FormatInt(published.Version, 10))
	form.Set("confirm_run", "yes")
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=stale-run")

	update := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/workflows/stored/stale-run", api.SaveStoredWorkflowDraftRequest{
		ExpectedVersion: published.Version, Definition: published.Draft,
	}, nil)
	if update.Code != http.StatusOK {
		t.Fatalf("concurrent draft update: %d %s", update.Code, update.Body.String())
	}
	response := consolePost(e, cookie, page, base+"/run", form)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale run: %d %s", response.Code, response.Body.String())
	}
	runs, _, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("stale page admitted a run: %d, %v", len(runs), err)
	}
}
