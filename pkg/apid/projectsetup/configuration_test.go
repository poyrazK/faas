package projectsetup

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateSelectedConfiguration(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	workloads := []reposcan.Workload{{Name: "api"}}
	for _, test := range []struct {
		name   string
		values api.ProjectInitialSecrets
		valid  bool
	}{
		{"selected", api.ProjectInitialSecrets{"api": {"DATABASE_URL": "sensitive-value"}}, true},
		{"unselected", api.ProjectInitialSecrets{"worker": {"DATABASE_URL": "sensitive-value"}}, false},
		{"invalid key", api.ProjectInitialSecrets{"api": {"bad-key": "sensitive-value"}}, false},
		{"empty", api.ProjectInitialSecrets{"api": {"DATABASE_URL": ""}}, false},
		{"over limit", api.ProjectInitialSecrets{"api": {"DATABASE_URL": strings.Repeat("x", limits.SecretValueMaxBytes+1)}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			problem := Validate(test.values, workloads, limits)
			if (problem == nil) != test.valid {
				t.Fatalf("valid=%v problem=%v", test.valid, problem)
			}
			if problem != nil && strings.Contains(problem.Detail, "sensitive-value") {
				t.Fatal("value leaked")
			}
		})
	}
}

type metadataStore struct{ rows map[string][]state.AppSecret }

func (s metadataStore) ListAllAppSecrets(_ context.Context, _, app string) ([]state.AppSecret, error) {
	return s.rows[app], nil
}

func TestApplyChecksEveryQuotaBeforeAnyWrite(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	rows := make([]state.AppSecret, limits.SecretCountMax)
	for i := range rows {
		rows[i] = state.AppSecret{Scope: "staging", Key: "OTHER"}
	}
	apps := []state.App{{ID: "first", AccountID: "account", WorkloadName: "api"}, {ID: "last", AccountID: "account", WorkloadName: "web"}}
	writes := 0
	problem := Apply(t.Context(), metadataStore{map[string][]state.AppSecret{"last": rows}}, "account", "default", apps,
		api.ProjectInitialSecrets{"api": {"DATABASE_URL": "sensitive"}, "web": {"DATABASE_URL": "sensitive"}}, limits,
		func(context.Context, state.App, string, string, string) *api.Problem { writes++; return nil })
	if problem == nil || writes != 0 {
		t.Fatalf("problem=%v writes=%d", problem, writes)
	}
}

func TestApplyAllowsOverwriteAtQuotaAndSanitizesFailure(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	limits.SecretCountMax = 1
	app := state.App{ID: "app", AccountID: "account", WorkloadName: "api"}
	writes := 0
	problem := Apply(t.Context(), metadataStore{map[string][]state.AppSecret{"app": {{Scope: "default", Key: "DATABASE_URL"}}}}, "account", "default", []state.App{app},
		api.ProjectInitialSecrets{"api": {"DATABASE_URL": "sensitive"}}, limits,
		func(_ context.Context, _ state.App, scope, key, value string) *api.Problem {
			writes++
			if scope != "default" || key != "DATABASE_URL" || value != "sensitive" {
				t.Fatal("wrong target")
			}
			return api.ErrCapacity("backend echoed sensitive")
		})
	if writes != 1 || problem == nil || problem.Code != "project_configuration_failed" || strings.Contains(problem.Detail, "sensitive") {
		t.Fatalf("writes=%d problem=%v", writes, problem)
	}
}

func TestExplicitRequirements(t *testing.T) {
	fsys := fstest.MapFS{
		".gregale/required-secrets.json":        {Data: []byte(`["DATABASE_URL","DATABASE_URL"]`)},
		"worker/.gregale/required-secrets.json": {Data: []byte(`["REDIS_URL"]`)},
		"broken/.gregale/required-secrets.json": {Data: []byte(`{"SECRET":"do-not-echo"}`)},
	}
	for root, want := range map[string]string{"api": "DATABASE_URL", "worker": "REDIS_URL"} {
		names, err := RequiredNames(fsys, root)
		if err != nil || len(names) != 1 || names[0] != want {
			t.Fatalf("root=%s names=%v err=%v", root, names, err)
		}
	}
	if _, err := RequiredNames(fsys, "broken"); err == nil || strings.Contains(err.Error(), "do-not-echo") {
		t.Fatalf("err=%v", err)
	}
	if names, err := RequiredNames(fstest.MapFS{}, "api"); err != nil || len(names) != 0 {
		t.Fatalf("names=%v err=%v", names, err)
	}
}
