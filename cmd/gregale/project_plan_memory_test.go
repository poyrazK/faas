package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestProjectPlanNamesServicesStartingAtTheDefaultMemory is the Hunt 14
// H14-36 regression: project services without a Compose memory limit got
// 128 MB while a standalone app on the same plan got 2048 MB, and the plan
// never said so.
func TestProjectPlanNamesServicesStartingAtTheDefaultMemory(t *testing.T) {
	plan := api.PlanResponse{
		ProjectSlug: "shop", CanApply: true,
		Workloads: []api.PlanWorkload{
			{Name: "web", AppSlug: "shop-web", RootDir: "web"},
			{Name: "api", AppSlug: "shop-api", RootDir: "api", Resources: &api.ComposeResources{MemoryMB: 512}},
			{Name: "old", AppSlug: "shop-old", RootDir: "old"},
		},
		WillDeploy: []api.PlanAffectedApp{
			{Slug: "shop-web", Action: "create"},
			{Slug: "shop-api", Action: "create"},
			{Slug: "shop-old", ID: "existing", Action: "update"},
		},
	}
	var out bytes.Buffer
	if code := main.PrintPlanTextWithExplain(&out, plan, nil, false, false); code != 0 {
		t.Fatalf("print plan exit = %d", code)
	}
	got := out.String()
	lineFor := func(name string) string {
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "- "+name+" ") {
				return line
			}
		}
		t.Fatalf("plan has no workload line for %s:\n%s", name, got)
		return ""
	}
	if line := lineFor("web"); !strings.Contains(line, "memory=128MB (default)") {
		t.Errorf("new service without Compose memory: %q, want memory=128MB (default)", line)
	}
	if line := lineFor("api"); !strings.Contains(line, "memory=512MB") || strings.Contains(line, "(default)") {
		t.Errorf("new service with Compose memory: %q, want memory=512MB", line)
	}
	if line := lineFor("old"); strings.Contains(line, "memory=") {
		t.Errorf("existing app keeps its RAM, but the plan names one: %q", line)
	}
	section := got[strings.Index(got, "Starts with the default 128 MB"):]
	if !strings.Contains(section, "  - web\n") || strings.Contains(section, "  - api\n") || strings.Contains(section, "  - old\n") {
		t.Fatalf("default-memory section should list only web:\n%s", section)
	}
	if !strings.Contains(section, "deploy.resources.limits.memory") || !strings.Contains(section, "gregale app <slug> --ram") {
		t.Fatalf("default-memory section does not say how to raise it:\n%s", section)
	}
}
