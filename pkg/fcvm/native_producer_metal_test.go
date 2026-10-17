//go:build metal && linux

package fcvm

// adr: 20261017
// Native producers must remain bound to the owning daemon lifetime.

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// The daemon dies after a real CLONE_INTO_CGROUP child has crossed its gate.
// The new owner must join that cgroup before the original slot is reusable.
func TestMetalDataRepairProducerRecovery(t *testing.T) {
	if os.Getenv("FAAS_DATA_REPAIR_NATIVE_TEST") != "1" {
		t.Skip("explicit native repair acceptance not selected")
	}
	qualificationNativeHost(t, t.Context())
	runNativeProducerRecovery(t, true)
	leakcheck.AssertZero(t)
}
