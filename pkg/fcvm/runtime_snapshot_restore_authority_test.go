// adr: 592
package fcvm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestNativeSnapshotRestoreBoundGrantRefusedBeforeAllocation(t *testing.T) {
	runner, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(runner, vmm)
	req := admittedFixture(t, m)
	req.Binding.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	req.Binding.ArtifactSourcesHash = strings.Repeat("a", 64)
	req.Binding.SnapshotCaptureToken = uuid.NewString()
	req.Binding.SnapshotEvidenceHash = strings.Repeat("b", 64)
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatal("restore binding silently entered cold path", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || vmm.bootCount != 0 || len(m.runtimeAdmissionTokens) != 0 || len(m.runtimeAdmissionFlights) != 0 {
		t.Fatal("restore refusal consumed or allocated native authority")
	}
	i, err := m.RuntimeAdmissionIdentity()
	if err != nil || i.SnapshotRestoreVersion != 0 {
		t.Fatal("unmeasured loader advertised restore capability", err)
	}
}
