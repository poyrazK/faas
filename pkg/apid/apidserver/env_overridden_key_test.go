package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// `env set PORT=9999` was accepted and the app still got the platform's PORT
// (prod hunt r78).
func TestOverriddenEnvKeyProblem(t *testing.T) {
	t.Parallel()
	for key, refused := range map[string]bool{
		"PORT":                   true,
		api.PlatformAppIDEnv:     true,
		"DATABASE_URL":           false,
		"PATH":                   false,
		"GREGALE_FEATURE_TOGGLE": false,
	} {
		if got := apidserver.OverriddenEnvKeyProblem(key) != nil; got != refused {
			t.Errorf("OverriddenEnvKeyProblem(%q) refused = %v, want %v", key, got, refused)
		}
	}
}
