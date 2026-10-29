package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestControlPlaneEnablesRouteProbesThroughTheLocalGateway pins ADR-847
// amendment 1. Production never set FAAS_ROUTE_PROBE_URL, so the ADR-1446
// default probe never ran and every quiet safe release waited
// CanaryLowTrafficMaxHold at each stage. The role turns probes on by default
// and points them at the control plane's own gatewayd-public listener.
func TestControlPlaneEnablesRouteProbesThroughTheLocalGateway(t *testing.T) {
	defaults := readRepoFile(t, "deploy", "ansible", "roles", "control_plane_service", "defaults", "main.yml")
	if !regexp.MustCompile(`(?m)^route_probes_enabled: true$`).MatchString(defaults) {
		t.Fatal("control_plane_service defaults must turn route probes on")
	}
	probeURL := regexp.MustCompile(`(?m)^faas_route_probe_url: (\S+)$`).FindStringSubmatch(defaults)
	if probeURL == nil {
		t.Fatal("control_plane_service defaults name no faas_route_probe_url")
	}
	origin, err := url.Parse(probeURL[1])
	if err != nil || origin.Scheme != "http" {
		t.Fatalf("faas_route_probe_url %q must be a plain-HTTP gateway origin", probeURL[1])
	}

	unit := readRepoFile(t, "deploy", "ansible", "roles", "gatewayd_public_service", "files", "faas-gatewayd-public.service")
	listen := regexp.MustCompile(`(?m)^Environment=FAAS_PUBLIC_LISTEN_ADDR=(\S+)$`).FindStringSubmatch(unit)
	if listen == nil {
		t.Fatal("faas-gatewayd-public.service names no FAAS_PUBLIC_LISTEN_ADDR")
	}
	if origin.Host != listen[1] {
		t.Errorf("route probes go to %s, but gatewayd-public listens on %s", origin.Host, listen[1])
	}

	dropIn := readRepoFile(t, "deploy", "ansible", "roles", "control_plane_service", "templates", "zz-faas-route-probes.conf.j2")
	for _, want := range []string{
		"{% if route_probes_enabled | default(true) | bool %}",
		"Environment=FAAS_ROUTE_PROBE_URL={{ faas_route_probe_url",
	} {
		if !strings.Contains(dropIn, want) {
			t.Errorf("zz-faas-route-probes.conf.j2 lacks %q", want)
		}
	}
}
