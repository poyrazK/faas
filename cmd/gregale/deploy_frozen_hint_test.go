package main_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A deploy refused by a release freeze, for an app without gregale.yaml,
// told the customer to resume it "to finish saved deployment settings
// cleanup" although nothing was staged (Hunt 12 H12-31).
func TestFrozenDeployHintWithoutStagedSettings(t *testing.T) {
	repo := main.InitZeroConfigRepo(t)
	main.MustGit(t, repo, "remote", "remove", "origin")
	main.WithCwd(t, repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodGet:
			main.WriteJSONTest(w, api.AppResponse{ID: "a1", Slug: "demo", Type: "app"})
		case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodPatch:
			main.WriteJSONTest(w, api.AppResponse{ID: "a1", Slug: "demo", Type: "app"})
		case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
			http.NotFound(w, r) // fall back to the multipart deploy
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
			api.WriteProblem(w, api.ErrReleaseFrozen("demo", []api.ReleaseFreeze{{ID: "f1", App: "demo", Kind: "window", EndsAt: "2026-10-22T14:15:37Z", Reason: "incident", CreatedBy: "alice"}}))
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_API", (*stub.Srv()).URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	main.WithResetJSONOutput(t, false)
	_, stderr, restore := main.SwapIO(t)
	code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait"})
	restore()
	got := stderr()
	if code == 0 || !strings.Contains(got, "Production releases are frozen") {
		t.Fatalf("exit %d, want the freeze refusal:\n%s", code, got)
	}
	if strings.Contains(got, "settings cleanup") {
		t.Fatalf("hint claims settings cleanup with nothing staged:\n%s", got)
	}
}
