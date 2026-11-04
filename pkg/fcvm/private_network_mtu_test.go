package fcvm

// adr: 20261103235901

import (
	"context"
	"net/netip"
	"testing"
)

type mtuCapture struct{ mtu string }

func (c mtuCapture) RunCapture(context.Context, []string) ([]byte, error) {
	return []byte("4: overlay0: state UP mtu " + c.mtu), nil
}

func TestPrivateTransportMTURejectsOversizedUnderlayAndDefaultsConsistently(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{Enabled: true, OverlayInterface: "overlay0", LocalAddress: netip.MustParseAddr("100.64.0.10")})
	m.WithCaptureRunner(mtuCapture{mtu: "1280"})
	if m.privateNetworkMTU() != 1230 {
		t.Fatal("unexpected default regional MTU")
	}
	if err := m.validatePrivateTransportMTU(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.WithCaptureRunner(mtuCapture{mtu: "1200"})
	if err := m.validatePrivateTransportMTU(t.Context()); err == nil {
		t.Fatal("undersized underlay accepted without fragmentation protection")
	}
}
