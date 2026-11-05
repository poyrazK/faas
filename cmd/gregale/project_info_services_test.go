package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026193018: projects info lists each service with what it calls.
func TestProjectInfoListsServicesAndWhatTheyCall(t *testing.T) {
	var out bytes.Buffer
	previousOut, previousJSON := *main.OsStdout, *main.JsonOutput
	*main.OsStdout, *main.JsonOutput = &out, false
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = previousOut, previousJSON })
	project := api.ProjectResponse{Workloads: []api.ProjectWorkloadResponse{
		{Slug: "shop-web", WorkloadName: "web", DeploymentStatus: "live", DependsOn: []string{"api", "worker"}},
		{Slug: "shop-api", WorkloadName: "api", DeploymentStatus: "live"},
		{Slug: "shop-worker", WorkloadName: "worker"},
	}}
	project.Slug = "shop"
	main.RenderProject(project)
	got := out.String()
	for _, want := range []string{
		"  services:\n",
		"    NAME    APP          DEPLOYMENT  CALLS\n",
		"    web     shop-web     live        api, worker\n",
		"    api     shop-api     live        -\n",
		"    worker  shop-worker  -           -\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("projects info missing %q:\n%s", want, got)
		}
	}
}
