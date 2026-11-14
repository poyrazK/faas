// adr: 20261112223000

package featurehints_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
)

func TestSourceHostsNameTheVariableToRead(t *testing.T) {
	in := featurehints.Input{ProjectSlug: "voting-app",
		Workloads: []api.PlanWorkload{
			{Name: "vote", AppSlug: "vote", SourceHosts: []api.PlanSourceHost{{Host: "redis", File: "vote/app.py", Line: 21}}},
			{Name: "result", AppSlug: "result", DependsOn: []string{"api"}, SourceHosts: []api.PlanSourceHost{
				{Host: "db", File: "result/server.js", Line: 21}, {Host: "api", File: "result/server.js", Line: 40}}},
		},
		Managed: []api.PlanManaged{
			{Name: "db", Kind: "postgres", Provision: &api.PlanManagedProvision{Database: "d", Bindings: []api.PlanManagedBinding{{Workload: "result", EnvKey: "DATABASE_URL"}}}},
			{Name: "redis", Kind: "redis"},
		}}
	var hint *api.FeatureHint
	for _, h := range featurehints.For(in) {
		if h.ID == featurehints.IDSourceHosts {
			hint = &h
		}
	}
	if hint == nil {
		t.Fatal("no source-hosts hint")
	}
	for _, want := range []string{`vote/app.py:21 ("redis")`, `result/server.js:21 ("db")`} {
		if !strings.Contains(hint.Applied, want) {
			t.Errorf("applied lacks %q: %s", want, hint.Applied)
		}
	}
	for _, want := range []string{"result reads DATABASE_URL for db", "vote reads REDIS_URL", "$GREGALE_SERVICE_API_HOST (raw TCP)"} {
		if !strings.Contains(hint.Change, want) {
			t.Errorf("change lacks %q: %s", want, hint.Change)
		}
	}
	if strings.Contains(hint.Change, "api once it lists") {
		t.Errorf("result already depends on api: %s", hint.Change)
	}
	if len(hint.Commands) != 1 || hint.Commands[0] != "gregale env set --app vote REDIS_URL=<external redis URL>" {
		t.Errorf("commands = %q", hint.Commands)
	}
}
