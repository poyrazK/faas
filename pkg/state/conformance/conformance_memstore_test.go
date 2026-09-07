package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the shared harness itself covered when the package is included in the
// repository-wide coverage profile. The state package also runs this contract
// through its external test package, while this test instruments the helper
// code so it does not lower the pkg/state coverage gate.
func TestMemStore(t *testing.T) {
	Run(t, func(_ *testing.T) state.Store {
		return state.NewMemStore()
	})
}
