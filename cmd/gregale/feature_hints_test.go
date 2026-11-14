// adr: 20261112223000

package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
)

// hintPlans exercise every rule, so every command a hint can print is checked.
func hintPlans() []featurehints.Input {
	return []featurehints.Input{
		{ProjectSlug: "voting-app", Workloads: []api.PlanWorkload{
			{Name: "vote", AppSlug: "vote", PublicURL: "https://vote.gregale.dev", PublicAuthMode: api.AppPublicAuthModeBearer, BuildTarget: "dev",
				ImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "true"}}, UnsetEnvKeys: []string{"OPTION_A"}},
			{Name: "result", AppSlug: "result", PublicURL: "https://result.gregale.dev", PublicAuthMode: api.AppPublicAuthModeBearer,
				Command: []string{"nodemon", "server.js"}, UnsetEnvKeys: []string{"OPTION_A"}},
			{Name: "cache", AppSlug: "cache", Visibility: api.AppVisibilityInternal},
			{Name: "worker", AppSlug: "worker", Class: "worker", Visibility: api.AppVisibilityInternal},
		}, Managed: []api.PlanManaged{
			{Name: "db", Kind: "postgres", Provision: &api.PlanManagedProvision{Database: "d", Bindings: []api.PlanManagedBinding{{Workload: "result", EnvKey: "DATABASE_URL"}}}},
			{Name: "redis", Kind: "redis", Unbound: []string{"vote.REDIS_URL"}},
		}},
		// No project slug or environment: placeholders stand in.
		{Workloads: []api.PlanWorkload{
			{Name: "alpha", Visibility: api.AppVisibilityInternal, UnsetEnvKeys: []string{"K"}},
			{Name: "beta", Visibility: api.AppVisibilityInternal, UnsetEnvKeys: []string{"K"}},
		}},
	}
}

func TestEveryHintCommandIsARealCommand(t *testing.T) {
	seen := map[string]bool{}
	for _, in := range hintPlans() {
		for _, hint := range featurehints.For(in) {
			seen[hint.ID] = true
			for _, command := range hint.Commands {
				if err := main.ResolveHintCommand(command); err != nil {
					t.Errorf("%s: %v", hint.ID, err)
				}
			}
		}
	}
	for _, id := range []string{featurehints.IDHealthcheck, featurehints.IDHealthDefault, featurehints.IDPublicNeedsKey, featurehints.IDPrivateServices,
		featurehints.IDManagedPostgres, featurehints.IDNotRunDatastore, featurehints.IDUnsetEnv, featurehints.IDDevSetup, featurehints.IDProjectReleases} {
		if !seen[id] {
			t.Errorf("no test plan produces hint %s, so its commands are unchecked", id)
		}
	}
}

func TestResolveHintCommandRefusesUnknownCommandsAndFlags(t *testing.T) {
	for _, line := range []string{"gregale deploy --project --no-such-flag", "gregale no-such-command x", "deploy --project"} {
		if err := main.ResolveHintCommand(line); err == nil {
			t.Errorf("%q: want an error", line)
		}
	}
}

func TestPlanTextShowsFeatureHints(t *testing.T) {
	plan := api.PlanResponse{ProjectSlug: "voting-app", CanApply: true, Workloads: hintPlans()[0].Workloads, Managed: hintPlans()[0].Managed}
	var out bytes.Buffer
	main.PrintPlanTextWithExplain(&out, plan, nil, false, false)
	text := out.String()
	for _, want := range []string{"For this repo (what Gregale will do, and how to change it):", "$ gregale app update vote --no-require-authn",
		"$ gregale logs --project voting-app --follow"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan text lacks %q:\n%s", want, text)
		}
	}
	// A server that already sent hints is not second-guessed.
	plan.FeatureHints = []api.FeatureHint{{ID: "server-hint", Applied: "from the server"}}
	out.Reset()
	main.PrintPlanTextWithExplain(&out, plan, nil, false, false)
	if !strings.Contains(out.String(), "from the server") || strings.Contains(out.String(), "--no-require-authn") {
		t.Errorf("server hints must be shown as sent:\n%s", out.String())
	}
}
