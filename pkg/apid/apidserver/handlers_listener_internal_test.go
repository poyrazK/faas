// adr: 576
package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An internal listener (compose expose:) never gets a public raw TCP or UDP
// endpoint; a public declaration of the same shape still does.
func TestPublicListenersRefuseInternalPorts(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{Ports: []api.WorkloadPort{
		{Port: 6379, Protocol: api.WorkloadPortTCP, Internal: true},
		{Port: 5353, Protocol: api.WorkloadPortUDP, Internal: true},
		{Port: 9100, Protocol: api.WorkloadPortTCP},
	}}}
	if apidserver.AppDeclaresTCPListener(app, "tcp-6379", 6379) {
		t.Fatal("internal TCP listener accepted for public raw TCP")
	}
	if apidserver.AppDeclaresUDPListener(app, "udp-5353", 5353) {
		t.Fatal("internal UDP listener accepted for public UDP")
	}
	if !apidserver.AppDeclaresTCPListener(app, "tcp-9100", 9100) {
		t.Fatal("public TCP listener refused")
	}
}

// A listener request naming a declared internal port is told why it is
// refused (ADR-20261112223000 §2): the eval's agent declared redis internal,
// then retried `tcp add` against the generic "declare the port" error.
func TestInternalTCPPortIsRecognized(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{Ports: []api.WorkloadPort{
		{Name: "redis", Port: 6379, Protocol: api.WorkloadPortTCP, Internal: true},
		{Port: 7000, Protocol: api.WorkloadPortTCP, Internal: true},
		{Port: 9100, Protocol: api.WorkloadPortTCP},
	}}}
	if port, ok := apidserver.AppInternalTCPPort(app, "Redis", 6379); !ok || port.Name != "redis" {
		t.Fatalf("redis = %+v, %v; want the internal port", port, ok)
	}
	if port, ok := apidserver.AppInternalTCPPort(app, "tcp-7000", 7000); !ok || port.Name != "tcp-7000" {
		t.Fatalf("unnamed = %+v, %v; want tcp-7000", port, ok)
	}
	for _, c := range []struct {
		name string
		port int
	}{{"redis", 6380}, {"tcp-9100", 9100}, {"other", 6379}} {
		if _, ok := apidserver.AppInternalTCPPort(app, c.name, c.port); ok {
			t.Errorf("%s:%d recognized as internal", c.name, c.port)
		}
	}
}
