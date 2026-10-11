//go:build !linux

package edgetopology

// adr: 707

import (
	"errors"
	"reflect"
	"testing"
)

func TestNativeActivationProductionCollectorRejectsNonLinux(t *testing.T) {
	f := newActivationFixture(t)
	got, err := NewNativeActivationProbe().Observe(t.Context(), f.review)
	if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
		t.Fatal(got, err)
	}
}
