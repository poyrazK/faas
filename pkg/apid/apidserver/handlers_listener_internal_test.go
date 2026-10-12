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
