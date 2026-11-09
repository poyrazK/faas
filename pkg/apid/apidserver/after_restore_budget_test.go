package apidserver_test

// adr: 20261109120000 — after_restore may use up to 4 s only with a managed binding.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAfterRestoreBoundBudgetRequiresManagedBinding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.CreateApp(t, e, "restore-budget")
	path := "/v1/apps/restore-budget"
	extended := map[string]any{"after_restore": map[string]any{"path": "/internal/after-restore", "timeout_ms": api.AfterRestoreHookBoundMaxTimeoutMS}}
	if res := e.Do(t, "PATCH", path, extended, nil); res.Code != 422 {
		t.Fatalf("app without a managed binding accepted a %d ms hook: %d %s", api.AfterRestoreHookBoundMaxTimeoutMS, res.Code, res.Body.String())
	}
	ordinary := map[string]any{"after_restore": map[string]any{"path": "/internal/after-restore", "timeout_ms": api.AfterRestoreHookMaxTimeoutMS}}
	if res := e.Do(t, "PATCH", path, ordinary, nil); res.Code != 200 {
		t.Fatalf("ordinary cap refused: %d %s", res.Code, res.Body.String())
	}
	tooLong := map[string]any{"after_restore": map[string]any{"path": "/internal/after-restore", "timeout_ms": api.AfterRestoreHookBoundMaxTimeoutMS + 1}}
	if res := e.Do(t, "PATCH", path, tooLong, nil); res.Code == 200 {
		t.Fatalf("hook above the bound cap accepted: %s", res.Body.String())
	}
}
