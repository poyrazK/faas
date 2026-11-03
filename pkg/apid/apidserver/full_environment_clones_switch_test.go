// adr: 590
package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The clone machinery cannot be turned off while admission is open or a clone
// is unfinished: the gateway would serve an unpublished stage and schedd would
// stop recording what the clone's retirement proofs read
// (ADR-20261023091049).
func TestFullEnvironmentClonesSwitchStaysOnWhileClonesRun(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	const machinery, admission = "full_environment_clones_enabled", "full_environment_clone_admission_enabled"
	patch := func(key string, value bool) int {
		t.Helper()
		return e.DoAdmin(t, http.MethodPatch, "/v1/admin/config/"+key, map[string]any{"value": value, "reason": "clone switch test"}, nil).Code
	}
	// Nothing runs yet, so off is accepted.
	if code := patch(machinery, false); code != http.StatusOK {
		t.Fatalf("idle switch off = %d", code)
	}
	if code := patch(machinery, true); code != http.StatusOK {
		t.Fatalf("switch on = %d", code)
	}
	if code := patch(admission, true); code != http.StatusOK {
		t.Fatalf("admission on = %d", code)
	}
	if code := patch(machinery, false); code != http.StatusConflict {
		t.Fatalf("switch off with admission open = %d", code)
	}
	if code := patch(admission, false); code != http.StatusOK {
		t.Fatalf("admission off = %d", code)
	}

	store := *e.Store()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "clone-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "clone-switch"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", SourceRevisionHash: strings.Repeat("a", 64), IdempotencyKey: "running"}); err != nil {
		t.Fatal(err)
	}
	if code := patch(machinery, false); code != http.StatusConflict {
		t.Fatalf("switch off with a running clone = %d", code)
	}
	// Rolling back to the first, off revision is refused the same way.
	rollback := e.DoAdmin(t, http.MethodPost, "/v1/admin/config/"+machinery+"/rollback", map[string]any{"version": 1, "reason": "clone switch test"}, nil)
	if rollback.Code != http.StatusConflict {
		t.Fatalf("rollback to off with a running clone = %d %s", rollback.Code, rollback.Body.String())
	}
}
