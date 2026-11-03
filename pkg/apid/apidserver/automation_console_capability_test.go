package apidserver_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Dashboard forwarding must respect the capability of the API operation as
// well as the Automations page's own capability.
func TestAutomationConsoleHonorsWorkflowCapability(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "automations"), platformflags.Definition{Enabled: true, Default: true})
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "gated-report")
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != http.StatusOK {
		t.Fatalf("save: %d %s", page.Code, page.Body.String())
	}
	draft := consoleVersion(t, e, app.Slug, "gated-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", page.Code, page.Body.String())
	}
	form.Set("expected_version", strconv.FormatInt(consoleVersion(t, e, app.Slug, "gated-report").Version, 10))
	form.Set("confirm_run", "yes")

	srv.SeedPlatformFlag(capabilityFlag(t, "workflows"), platformflags.Definition{Enabled: true, Default: false})
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=gated-report")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Workflows are unavailable for this account") {
		t.Fatalf("workflow readiness: %d %s", page.Code, page.Body.String())
	}
	denied := consolePost(e, cookie, page, base+"/run", form)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("run while workflows are disabled: %d %s", denied.Code, denied.Body.String())
	}
	runs, _, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("disabled workflows admitted a run: %d, %v", len(runs), err)
	}

	srv.SeedPlatformFlag(capabilityFlag(t, "workflows"), platformflags.Definition{Enabled: true, Default: true})
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=gated-report")
	accepted := consolePost(e, cookie, page, base+"/run", form)
	if accepted.Code != http.StatusSeeOther {
		t.Fatalf("run after enabling workflows: %d %s", accepted.Code, accepted.Body.String())
	}
}
