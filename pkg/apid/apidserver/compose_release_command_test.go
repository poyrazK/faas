// adr: 20261102192943
package apidserver_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceArchive writes files under a GitHub-style top-level directory.
func sourceArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	prefixed := make(map[string]string, len(files))
	for name, body := range files {
		prefixed["gregale-source-main/"+name] = body
	}
	return writeSourceArchive(t, prefixed)
}

// Every deploy path of a project service runs the release command its
// Compose file gave it, unless the service's own source declares one.
func TestResolveSourceReleaseCommandKeepsComposeReleaseCommand(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{ProjectReleaseCommand: []string{"npm", "run", "migrate"}}}
	for _, tc := range []struct {
		name     string
		files    map[string]string
		manifest *gregalemanifest.Manifest
		want     []string
		shell    bool
	}{
		{"no declaration", map[string]string{"server.js": "x"}, nil, []string{"npm", "run", "migrate"}, false},
		{"procfile without release", map[string]string{"Procfile": "web: node server.js\n"}, nil, []string{"npm", "run", "migrate"}, false},
		{"procfile release wins", map[string]string{"Procfile": "web: node server.js\nrelease: npm run db:migrate\n"}, nil, []string{"npm run db:migrate"}, true},
		{"gregale.yaml release wins", map[string]string{"server.js": "x"}, &gregalemanifest.Manifest{Release: &gregalemanifest.ReleaseConfig{Command: "make migrate"}}, []string{"make migrate"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, shell, problem := apidserver.ResolveSourceReleaseCommand(sourceArchive(t, tc.files), app, tc.manifest)
			if problem != nil {
				t.Fatal(problem.Detail)
			}
			if !slices.Equal(command, tc.want) || shell != tc.shell {
				t.Fatalf("release = %v shell=%v, want %v shell=%v", command, shell, tc.want, tc.shell)
			}
		})
	}
	// An app without a Compose release command keeps having none.
	command, _, problem := apidserver.ResolveSourceReleaseCommand(sourceArchive(t, map[string]string{"server.js": "x"}), state.App{}, nil)
	if problem != nil || command != nil {
		t.Fatalf("release = %v, %v; want none", command, problem)
	}
}
