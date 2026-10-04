package runtimequotahost

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHostEpochDiscoveryRefusesClosedOrUncertainOwner(t *testing.T) {
	for _, state := range []string{"closed", "uncertain-sync"} {
		t.Run(state, func(t *testing.T) {
			j, _, p := fixture(t)
			epoch, err := j.RuntimeHostEpoch()
			if err != nil || epoch.NodeID != p.Permit.NodeID || epoch.HostEpochID != p.Permit.HostEpochID {
				t.Fatalf("current discovery: %+v %v", epoch, err)
			}
			if state == "closed" {
				if err := j.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				j.beforeSync = func() error { return errors.New("uncertain directory sync") }
				if _, err := j.FencePermit(p.Permit, uuid.NewString()); !errors.Is(err, ErrStorage) {
					t.Fatalf("uncertain write: %v", err)
				}
			}
			if epoch, err := j.RuntimeHostEpoch(); !errors.Is(err, ErrStorage) || epoch.HostEpochID != "" || epoch.NodeID != "" {
				t.Fatalf("unhealthy owner advertised destination: %+v %v", epoch, err)
			}
		})
	}
}
