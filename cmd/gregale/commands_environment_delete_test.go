package main_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-36: apid had DELETE /v1/projects/{slug}/environments/{env} but
// the CLI had no command, so a stage environment could not be removed. The
// command needs --yes and sends nothing without it.
func TestEnvironmentDeleteSendsDeleteWithConfirmation(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, ``, http.StatusNoContent)
	if code := main.CmdProjectsEnvironments([]string{"delete", "shop", "staging", "--yes"}); code != 0 {
		t.Fatalf("environment delete exit = %d, want 0", code)
	}
	if *f.SawMethod() != http.MethodDelete || *f.SawPath() != "/v1/projects/shop/environments/staging" {
		t.Fatalf("request = %s %s, want DELETE /v1/projects/shop/environments/staging", *f.SawMethod(), *f.SawPath())
	}

	f = main.AuthedFakeAPI(t, ``, http.StatusNoContent)
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdProjectsEnvironments([]string{"rm", "shop", "staging"}); code != 1 || *f.SawMethod() != "" {
		t.Fatalf("delete without --yes exit = %d request = %q, want usage and no request", code, *f.SawMethod())
	}
}
