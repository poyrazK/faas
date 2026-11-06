// adr: 20261019120007
package apidserver_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A service of a multi-service project runs the release command it declares in
// its own directory. `gregale deploy --project` used to drop it without a
// word, so a project deployed that way could never run its migration.
func TestProjectWorkloadReleaseCommandHonorsServiceDeclarations(t *testing.T) {
	gateway := reposcan.Workload{Name: "gateway", RootDir: "services/gateway"}
	store := reposcan.Workload{Name: "store", RootDir: "services/store"}
	migrate := &gregalemanifest.Manifest{Release: &gregalemanifest.ReleaseConfig{Command: "node migrate.mjs"}}
	app := state.App{RootDir: "services/gateway"}
	for _, tc := range []struct {
		name      string
		workloads []reposcan.Workload
		workload  reposcan.Workload
		files     map[string]string
		manifest  *gregalemanifest.Manifest
		want      []string
		wantErr   string
	}{
		{name: "gregale.yaml in own directory", workloads: []reposcan.Workload{gateway, store}, workload: gateway,
			files: map[string]string{"services/gateway/app.mjs": "x"}, manifest: migrate, want: []string{"node migrate.mjs"}},
		{name: "Procfile in own directory", workloads: []reposcan.Workload{gateway, store}, workload: gateway,
			files: map[string]string{"services/gateway/Procfile": "web: node app.mjs\nrelease: node migrate.mjs\n"}, want: []string{"node migrate.mjs"}},
		{name: "no declaration keeps none", workloads: []reposcan.Workload{gateway, store}, workload: gateway,
			files: map[string]string{"services/gateway/app.mjs": "x"}},
		{name: "compose release kept", workloads: []reposcan.Workload{gateway, store},
			workload: reposcan.Workload{Name: "gateway", RootDir: "services/gateway", ReleaseCommand: []string{"npm", "run", "migrate"}},
			files:    map[string]string{"services/gateway/app.mjs": "x"}, want: []string{"npm", "run", "migrate"}},
		{name: "shared directory refused",
			workloads: []reposcan.Workload{gateway, {Name: "worker", RootDir: "services/gateway/"}}, workload: gateway,
			files: map[string]string{"services/gateway/app.mjs": "x"}, manifest: migrate, wantErr: `services "gateway" and "worker" both build from that directory`},
		{name: "own declaration wins over compose step", workloads: []reposcan.Workload{gateway, store},
			workload: reposcan.Workload{Name: "gateway", RootDir: "services/gateway", ReleaseCommand: []string{"npm", "run", "migrate"}},
			files:    map[string]string{"services/gateway/app.mjs": "x"}, manifest: migrate, want: []string{"node migrate.mjs"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, _, problem := apidserver.ProjectWorkloadReleaseCommand(sourceArchive(t, tc.files), tc.workloads, tc.workload, app, tc.manifest)
			if tc.wantErr != "" {
				if problem == nil || !strings.Contains(problem.Detail, tc.wantErr) {
					t.Fatalf("problem = %+v, want detail containing %q", problem, tc.wantErr)
				}
				return
			}
			if problem != nil {
				t.Fatal(problem.Detail)
			}
			if !slices.Equal(command, tc.want) {
				t.Fatalf("release = %v, want %v", command, tc.want)
			}
		})
	}
}

// The repository root Procfile stays reposcan's: it already assigned its
// release: process to exactly one workload, so the other services must not
// pick it up again.
func TestProjectWorkloadReleaseCommandLeavesRootProcfileToReposcan(t *testing.T) {
	web := reposcan.Workload{Name: "web"}
	worker := reposcan.Workload{Name: "worker", RootDir: "worker"}
	archive := sourceArchive(t, map[string]string{"Procfile": "web: node app.js\nrelease: node migrate.js\n"})
	command, _, problem := apidserver.ProjectWorkloadReleaseCommand(archive, []reposcan.Workload{web, worker}, web, state.App{}, nil)
	if problem != nil || command != nil {
		t.Fatalf("release = %v, %v; want none (reposcan owns the root Procfile)", command, problem)
	}
}
