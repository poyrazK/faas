package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type standardConfigTestStore interface {
	standardRuntimeCaptureTestStore
	InstanceApplicationStandardConfigPublisher
	InstanceApplicationStandardRuntimeReceiptStore
	RuntimeConfigReceiptStore
}

func TestMemApplicationStandardRuntimeConfigPublication(t *testing.T) {
	standardRuntimeConfigPublication(t, NewMemStore())
}

func standardRuntimeConfigPublication(t *testing.T, s standardConfigTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, _, receipt := nativeBootTestAttempt(t, s, f, StateColdBooting)
	inputs := RuntimeConfigInputs{Scope: normalizedDeploymentScope(f.dep.Scope), Boundary: time.Now().UTC().Truncate(time.Microsecond)}
	for _, test := range []struct {
		name, wakeID, scope string
	}{
		{"wrong wake", uuid.NewString(), inputs.Scope},
		{"wrong scope", ins.WakeID, "preview"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := inputs
			bad.Scope = test.scope
			_, err := s.PublishInstanceApplicationStandardRuntimeWithConfig(t.Context(), ins.State, StateRunning, receipt, test.wakeID, bad)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("mismatched config publication: %v", err)
			}
			assertNativeBootUnpublished(t, s, ins)
			if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID); err == nil {
				t.Fatal("rejected config committed native receipt")
			}
			if _, exists, err := s.InstanceRuntimeConfigReceipt(t.Context(), ins.ID); err != nil || exists {
				t.Fatalf("rejected config committed input receipt: %v %v", exists, err)
			}
		})
	}
	actual, err := s.PublishInstanceApplicationStandardRuntimeWithConfig(t.Context(), ins.State, StateRunning, receipt, ins.WakeID, inputs)
	if err != nil || actual.State != string(StateRunning) || actual.WakeID != ins.WakeID {
		t.Fatalf("combined publication: %+v %v", actual, err)
	}
	native, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID)
	if err != nil || !native.Equal(receipt) {
		t.Fatalf("native publication missing: %v", err)
	}
	config, exists, err := s.InstanceRuntimeConfigReceipt(t.Context(), ins.ID)
	if err != nil || !exists || config.Scope != inputs.Scope || !config.Boundary.Equal(inputs.Boundary) {
		t.Fatalf("config publication missing: %+v %v %v", config, exists, err)
	}
}
