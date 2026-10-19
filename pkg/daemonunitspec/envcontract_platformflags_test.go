package daemonunitspec

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/platformflags"
)

// TestEnvContract_PlatformFlagFallbacksDeclared keeps each platform flag's
// boot-fallback variables (ADR-1700) in the contract: owned by every daemon
// that consumes the flag, and by "shared" because pkg/platformflags names them.
func TestEnvContract_PlatformFlagFallbacksDeclared(t *testing.T) {
	declared := map[string][]string{}
	for _, v := range EnvContract {
		declared[v.Name] = v.Owners
	}
	for _, spec := range platformflags.Catalog() {
		for _, name := range spec.LegacyEnv {
			owners, ok := declared[name]
			if !ok {
				t.Errorf("%s: boot fallback %s is not in EnvContract", spec.Key, name)
				continue
			}
			for _, want := range append([]string{"shared"}, spec.Consumers...) {
				if !slices.Contains(owners, want) {
					t.Errorf("%s: %s owners %v lack %q", spec.Key, name, owners, want)
				}
			}
		}
	}
}
