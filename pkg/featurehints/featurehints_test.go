// adr: 20261112223000

package featurehints_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
)

// votingApp is the plan dockersamples/example-voting-app produced on
// 2026-11-12: two public bearer web apps (vote builds the dev target, result
// runs nodemon), a worker, managed Postgres for db and Redis not run.
func votingApp() featurehints.Input {
	return featurehints.Input{
		ProjectSlug: "voting-app",
		Workloads: []api.PlanWorkload{
			{Name: "vote", AppSlug: "vote", PublicURL: "https://vote.gregale.dev", PublicAuthMode: api.AppPublicAuthModeBearer, Class: "http",
				BuildTarget: "dev", ImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "curl", "-f", "http://localhost"}}},
			{Name: "result", AppSlug: "result", PublicURL: "https://result.gregale.dev", PublicAuthMode: api.AppPublicAuthModeBearer, Class: "http",
				Command: []string{"nodemon", "--inspect=0.0.0.0", "server.js"}},
			{Name: "worker", AppSlug: "worker", Class: "worker", Visibility: api.AppVisibilityInternal},
		},
		Managed: []api.PlanManaged{
			{Name: "db", Kind: "postgres", Provision: &api.PlanManagedProvision{Database: "voting-app-db",
				Bindings: []api.PlanManagedBinding{{Workload: "result", EnvKey: "DATABASE_URL"}, {Workload: "worker", EnvKey: "DATABASE_URL"}}}},
			{Name: "redis", Kind: "redis", EnvHint: "REDIS_URL", Unbound: []string{"vote.REDIS_URL", "worker.REDIS_URL"}},
		},
	}
}

func byID(hints []api.FeatureHint) map[string]api.FeatureHint {
	out := map[string]api.FeatureHint{}
	for _, h := range hints {
		out[h.ID] = h
	}
	return out
}

func TestVotingAppHints(t *testing.T) {
	hints := featurehints.For(votingApp())
	var ids []string
	for _, h := range hints {
		ids = append(ids, h.ID)
	}
	want := []string{featurehints.IDHealthcheck, featurehints.IDHealthDefault, featurehints.IDPublicNeedsKey, featurehints.IDDevSetup,
		featurehints.IDProjectReleases, featurehints.IDManagedPostgres, featurehints.IDNotRunDatastore}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("hint IDs = %v, want %v", ids, want)
	}
	got := byID(hints)

	if w := got[featurehints.IDHealthcheck].Workloads; strings.Join(w, ",") != "vote" {
		t.Errorf("healthcheck workloads = %v, want [vote]", w)
	}
	// result has no healthcheck; the worker never listens, so it gets none.
	if w := got[featurehints.IDHealthDefault].Workloads; strings.Join(w, ",") != "result" {
		t.Errorf("health-default workloads = %v, want [result]", w)
	}
	public := got[featurehints.IDPublicNeedsKey]
	if strings.Join(public.Commands, "|") != "gregale app update result --no-require-authn|gregale app update vote --no-require-authn" {
		t.Errorf("public commands = %v", public.Commands)
	}
	if !strings.Contains(public.Change, "does not take --no-require-authn") {
		t.Errorf("public change must say the project deploy flag does not apply: %q", public.Change)
	}
	dev := got[featurehints.IDDevSetup]
	if !strings.Contains(dev.Applied, "vote builds the dev target") || !strings.Contains(dev.Applied, "result runs nodemon") {
		t.Errorf("dev setup = %q", dev.Applied)
	}
	pg := got[featurehints.IDManagedPostgres]
	if !strings.Contains(pg.Applied, "result.DATABASE_URL, worker.DATABASE_URL") || !strings.Contains(pg.Applied, `"db"`) {
		t.Errorf("managed postgres = %q", pg.Applied)
	}
	redis := got[featurehints.IDNotRunDatastore]
	if strings.Join(redis.Commands, "|") != "gregale env set --app vote REDIS_URL=<external redis URL>|gregale env set --app worker REDIS_URL=<external redis URL>" {
		t.Errorf("redis commands = %v", redis.Commands)
	}
	releases := got[featurehints.IDProjectReleases]
	if releases.Commands[len(releases.Commands)-1] != "gregale logs --project voting-app --follow" {
		t.Errorf("project release commands = %v", releases.Commands)
	}
}

func TestPrivateServicesAndUnsetEnv(t *testing.T) {
	in := featurehints.Input{ProjectSlug: "shop", Environment: "production", Workloads: []api.PlanWorkload{
		{Name: "ui", AppSlug: "ui", PublicURL: "https://ui.gregale.dev", PublicAuthMode: "open", UnsetEnvKeys: []string{"DB_PASSWORD"}},
		{Name: "orders", AppSlug: "orders", Visibility: api.AppVisibilityInternal, UnsetEnvKeys: []string{"DB_PASSWORD", "QUEUE_URL"}},
		{Name: "catalog", AppSlug: "catalog", Visibility: api.AppVisibilityInternal},
	}}
	got := byID(featurehints.For(in))
	if _, ok := got[featurehints.IDPublicNeedsKey]; ok {
		t.Error("an open public app needs no API-key hint")
	}
	private := got[featurehints.IDPrivateServices]
	if strings.Join(private.Workloads, ",") != "catalog,orders" ||
		private.Commands[0] != "gregale logs --project shop --service catalog,orders" {
		t.Errorf("private = %+v", private)
	}
	env := got[featurehints.IDUnsetEnv]
	want := []string{"gregale env set --app orders DB_PASSWORD=<value>", "gregale env set --app orders QUEUE_URL=<value>",
		"gregale environment secrets set shop production KEY=<value>"}
	if strings.Join(env.Commands, "|") != strings.Join(want, "|") {
		t.Errorf("unset env commands = %v, want %v", env.Commands, want)
	}
	if !strings.Contains(env.Applied, "DB_PASSWORD, QUEUE_URL") {
		t.Errorf("unset env applied = %q", env.Applied)
	}
}

func TestQuietPlansGetNoHints(t *testing.T) {
	in := featurehints.Input{ProjectSlug: "solo", Workloads: []api.PlanWorkload{
		{Name: "api", AppSlug: "api", PublicURL: "https://api.gregale.dev", PublicAuthMode: "open", Image: "ghcr.io/x/api:1"},
	}}
	if hints := featurehints.For(in); len(hints) != 0 {
		t.Fatalf("a single prebuilt open app should get no hints, got %+v", hints)
	}
	// A NONE healthcheck disables the image's check; it is not a declared one.
	none := featurehints.Input{Workloads: []api.PlanWorkload{{Name: "web", ImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"NONE"}}}}}
	if got := byID(featurehints.For(none)); got[featurehints.IDHealthcheck].ID != "" || got[featurehints.IDHealthDefault].ID == "" {
		t.Fatalf("NONE must count as no healthcheck: %+v", got)
	}
}
