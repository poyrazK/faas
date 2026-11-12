// A deployment that declares a release command is refused at submit, and the
// release-commands capability is hidden, while FAAS_RELEASE_PHASE_ENABLED is
// off on apid: imaged would fail it after its build (production 2026-11-04).
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleasePhaseOffRefusesReleaseDeclarations(t *testing.T) {
	compose := state.App{Manifest: state.AppManifest{ProjectReleaseCommand: []string{"npm", "run", "migrate"}}}
	for _, tc := range []struct {
		name     string
		files    map[string]string
		app      state.App
		manifest *gregalemanifest.Manifest
		refused  bool
	}{
		{"procfile release", map[string]string{"Procfile": "web: node server.js\nrelease: npm run db:migrate\n"}, state.App{}, nil, true},
		{"gregale.yaml release", map[string]string{"server.js": "x"}, state.App{}, &gregalemanifest.Manifest{Release: &gregalemanifest.ReleaseConfig{Command: "make migrate"}}, true},
		{"compose release", map[string]string{"server.js": "x"}, compose, nil, true},
		{"no declaration", map[string]string{"server.js": "x"}, state.App{}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := sourceArchive(t, tc.files)
			command, problem := apidserver.ResolveSourceReleaseCommandWithPhase(source, tc.app, tc.manifest, false)
			if !tc.refused {
				if problem != nil || command != nil {
					t.Fatalf("release = %v, %v; want none", command, problem)
				}
				return
			}
			if problem == nil || problem.Code != api.CodeReleasePhaseUnavailable || problem.Status != http.StatusUnprocessableEntity || command != nil {
				t.Fatalf("release = %v, %+v; want 422 %s", command, problem, api.CodeReleasePhaseUnavailable)
			}
			// The same declaration resolves once the release phase is on.
			if command, problem := apidserver.ResolveSourceReleaseCommandWithPhase(source, tc.app, tc.manifest, true); problem != nil || len(command) == 0 {
				t.Fatalf("enabled release = %v, %v", command, problem)
			}
		})
	}
}

func TestReleaseCommandsCapabilityFollowsTheReleasePhase(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		recorder := apidserver.CapabilitiesWithReleasePhase(enabled)
		var response api.CapabilitiesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(response.Capabilities, func(c api.CapabilityStatus) bool { return c.Key == "release-commands" })
		if i < 0 {
			t.Fatal("release-commands missing from the capabilities")
		}
		if got := response.Capabilities[i].Enabled; got != enabled {
			t.Fatalf("release phase %t: release-commands enabled = %t", enabled, got)
		}
	}
}

func TestReleasePhaseEnabledFromEnvParsesLikeImaged(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "1": true, "true": true, " TRUE ": true, "yes": false} {
		if got := apidserver.ReleasePhaseEnabledFromEnv(func(string) string { return value }); got != want {
			t.Errorf("%q: %t; want %t", value, got, want)
		}
	}
}

// Hunt 33 H33-54: a function app's release command hung its deploy in
// snapshotting (imaged had no image to run it in); it is refused at submit.
func TestFunctionAppReleaseCommandIsRefused(t *testing.T) {
	function := state.App{Type: state.AppTypeFunction}
	for _, tc := range []struct {
		name     string
		files    map[string]string
		manifest *gregalemanifest.Manifest
	}{
		{"gregale.yaml release", map[string]string{"handler.js": "x"}, &gregalemanifest.Manifest{Release: &gregalemanifest.ReleaseConfig{Command: "node migrate.js"}}},
		{"procfile release", map[string]string{"Procfile": "release: node migrate.js\n"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, problem := apidserver.ResolveSourceReleaseCommandWithPhase(sourceArchive(t, tc.files), function, tc.manifest, true)
			if problem == nil || problem.Status != http.StatusUnprocessableEntity || command != nil {
				t.Fatalf("release = %v, %+v; want a 422 refusal", command, problem)
			}
		})
	}
	command, problem := apidserver.ResolveSourceReleaseCommandWithPhase(sourceArchive(t, map[string]string{"handler.js": "x"}), function, nil, true)
	if problem != nil || command != nil {
		t.Fatalf("function without a release command = %v, %+v; want none", command, problem)
	}
}
