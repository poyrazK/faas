package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-741: the developer marker is platform identity. It is "1" only for a
// `gregale dev` environment and always emitted after customer entries, so a
// customer value for the reserved key cannot mark a production app.
func TestAppendPlatformIdentityDeveloperMarker(t *testing.T) {
	spoof := []fcvm.APIEnvEntry{{Key: api.PlatformDeveloperEnvironmentEnv, Value: "1"}}
	last := func(entries []fcvm.APIEnvEntry) string {
		value := "<missing>"
		for _, entry := range entries {
			if entry.Key == api.PlatformDeveloperEnvironmentEnv {
				value = entry.Value
			}
		}
		return value
	}
	for _, tc := range []struct {
		name string
		app  state.App
		want string
	}{
		{name: "production", app: state.App{ID: "app"}, want: ""},
		{name: "pull request preview", app: state.App{ID: "app", PreviewOfSlug: "api", PreviewPrNumber: 7}, want: ""},
		{name: "developer environment", app: state.App{ID: "app", PreviewOfSlug: "api"}, want: "1"},
	} {
		got := last(appendPlatformIdentity(spoof, tc.app, state.Deployment{}, state.Account{}, "node", "inst", "eu"))
		if got != tc.want {
			t.Errorf("%s: %s = %q, want %q", tc.name, api.PlatformDeveloperEnvironmentEnv, got, tc.want)
		}
	}
}
