package fcvm

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

func TestRegionalHostEpochDiscoveryRequiresHealthyJournal(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if _, err := m.RegionalRuntimeHostEpoch(); !errors.Is(err, runtimequotahost.ErrStorage) {
		t.Fatalf("disabled owner advertised epoch: %v", err)
	}
	m, j, req := regionalHostFixture(t, &fakeVMM{})
	epoch, err := m.RegionalRuntimeHostEpoch()
	if err != nil || epoch.NodeID != req.RegionalRuntime.Permit.NodeID || epoch.HostEpochID != req.RegionalRuntime.Permit.HostEpochID {
		t.Fatalf("Manager lost journal incarnation: %+v %v", epoch, err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RegionalRuntimeHostEpoch(); !errors.Is(err, runtimequotahost.ErrStorage) {
		t.Fatalf("closed owner advertised epoch: %v", err)
	}
}
