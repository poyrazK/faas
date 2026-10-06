package e2etest

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestGatewayMetalBuilderConfigEnablesPinnedGuestDNS(t *testing.T) {
	for _, guestDNS := range []bool{false, true} {
		var config struct {
			PublicAddr         string `toml:"public_addr"`
			ControlAddr        string `toml:"control_addr"`
			APIDLoopback       string `toml:"apid_loopback"`
			ServiceProxyListen string `toml:"service_proxy_listen"`
			NodeName           string `toml:"node_name"`
		}
		_, err := toml.Decode(gatewaydConfig("127.0.0.1:8080", "127.0.0.1:9090", "http://127.0.0.1:8081", guestDNS), &config)
		if err != nil {
			t.Fatal(err)
		}
		if config.PublicAddr != "127.0.0.1:8080" || config.ControlAddr != "127.0.0.1:9090" || config.APIDLoopback != "http://127.0.0.1:8081" {
			t.Fatalf("gateway listeners changed: %+v", config)
		}
		if !guestDNS {
			if config.ServiceProxyListen != "" || config.NodeName != "" {
				t.Fatalf("ordinary CI enabled a host-global bridge listener: %+v", config)
			}
			continue
		}
		want := net.JoinHostPort(api.DefaultHostBridgeCIDR().Addr().Next().String(), strconv.Itoa(netns.ServiceProxyPort))
		if config.ServiceProxyListen != want || strings.HasPrefix(config.ServiceProxyListen, "0.0.0.0:") {
			t.Fatalf("guest service/DNS listener = %q, want private bridge %q", config.ServiceProxyListen, want)
		}
		if config.NodeName != "default-local" {
			t.Fatalf("guest DNS has no seeded local VMMD identity for resolved egress: %+v", config)
		}
	}
}
