//go:build !linux

package edgetopology

// adr: 706

import (
	"errors"
	"reflect"
	"testing"
)

func TestNativeOriginsNonLinuxCannotStartNetworkObservation(t *testing.T) {
	f := newNativeOriginFixture(t)
	f.probe.open = newNativeScopeSession
	got, err := f.probe.Observe(t.Context(), f.review)
	if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) || f.caddyCalls.Load() != 0 || f.backendCalls.Load() != 0 || f.dns.dnsCalls.Load() != 0 || f.dns.apiCalls.Load() != 4 {
		t.Fatal(got, err, f.caddyCalls.Load(), f.backendCalls.Load(), f.dns.apiCalls.Load())
	}
}
