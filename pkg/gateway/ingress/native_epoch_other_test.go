//go:build !linux

package ingress

// adr: 710

import (
	"errors"
	"testing"
)

func TestNativeStartupConstructorFailsClosedOnUnsupportedPlatform(t *testing.T) {
	h, startup, err := NewNativePublicIdentityHandler(t.Context(), testToken, publicIdentityFixture())
	if !errors.Is(err, ErrNativeStartupUnverified) || h != nil || startup != (NativePublicStartup{}) {
		t.Fatal(h, startup, err)
	}
}
