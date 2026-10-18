// adr: 20261017201005
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestRehearsalNetworkDNSPortAgreement(t *testing.T) {
	if gateway.RehearsalServiceDiscoveryDNSPort != netns.RehearsalServiceDiscoveryDNSPort {
		t.Fatal("gateway listener and runtime DNS redirection disagree")
	}
}
