package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// The in-guest metadata proxy serves event publish, metadata env and secret
// reload acknowledgements on the link-local metadata address. It normally
// listens on port 80. Linux refuses a wildcard bind while another socket
// listens on the same port, so a workload that serves port 80 itself (nginx,
// httpd) would fail with EADDRINUSE. In that guest the proxy moves to an
// alternate port; workloads learn every URL from their environment.
const (
	metadataHost                = "169.254.169.254"
	metadataDefaultPort         = 80
	eventPublishPath            = "/v1/events:publish"
	metadataEnvPath             = "/v1/metadata/env"
	metadataSecretReloadAckPath = "/v1/metadata/secrets/reload-ack"
)

// metadataProxyAlternatePorts are tried in order when a workload declares
// port 80.
var metadataProxyAlternatePorts = []int{8775, 8776, 8777}

// The current proxy address and the endpoints stamped into workload
// environments. setMetadataProxyPort moves them together.
var (
	metadataListenAddr              string
	EventPublishEndpoint            string
	metadataEnvEndpoint             string
	metadataSecretReloadAckEndpoint string
)

func init() { setMetadataProxyPort(metadataDefaultPort) }

func setMetadataProxyPort(port int) {
	base := "http://" + metadataHost
	if port != metadataDefaultPort {
		base += ":" + strconv.Itoa(port)
	}
	metadataListenAddr = metadataHost + ":" + strconv.Itoa(port)
	EventPublishEndpoint = base + eventPublishPath
	metadataEnvEndpoint = base + metadataEnvPath
	metadataSecretReloadAckEndpoint = base + metadataSecretReloadAckPath
}

// metadataProxyPort keeps the proxy on port 80 unless a workload uses it.
func metadataProxyPort(workloadPorts map[int]bool) int {
	if !workloadPorts[metadataDefaultPort] {
		return metadataDefaultPort
	}
	for _, port := range metadataProxyAlternatePorts {
		if !workloadPorts[port] {
			return port
		}
	}
	return metadataDefaultPort
}

// workloadPortSpec is the port subset of the main manifest and of each
// roster entry; companions share the guest's loopback with the main app.
type workloadPortSpec struct {
	Port  int                `json:"port"`
	Ports []api.WorkloadPort `json:"ports,omitempty"`
}

// metadataWorkloadTCPPorts collects the TCP ports the guest's workloads
// serve: the main manifest's serving port and declared listeners, and every
// workload in the roster. Missing or unreadable files contribute nothing.
func metadataWorkloadTCPPorts(root fs.FS) map[int]bool {
	ports := map[int]bool{}
	add := func(spec workloadPortSpec) {
		if spec.Port > 0 {
			ports[spec.Port] = true
		}
		for _, p := range spec.Ports {
			if p.Port > 0 && (p.Protocol == "" || p.Protocol == api.WorkloadPortTCP) {
				ports[p.Port] = true
			}
		}
	}
	if data, err := fs.ReadFile(root, strings.TrimPrefix(api.AppManifestPath, "/")); err == nil {
		if manifest, err := api.ReadManifest(bytes.NewReader(data)); err == nil {
			add(workloadPortSpec{Port: manifest.EffectivePort(), Ports: manifest.Ports})
		}
	}
	if data, err := fs.ReadFile(root, strings.TrimPrefix(workloadRosterFile, "/")); err == nil {
		var roster struct {
			Main     workloadPortSpec   `json:"main"`
			Sidecars []workloadPortSpec `json:"sidecars"`
		}
		if json.Unmarshal(data, &roster) == nil {
			add(roster.Main)
			for _, sidecar := range roster.Sidecars {
				add(sidecar)
			}
		}
	}
	return ports
}

// workloadRosterFile mirrors workloadRosterPath (Linux-only) so the port
// selection stays testable on every platform.
const workloadRosterFile = "/etc/faas/workloads.json"
