package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

// A workload that serves port 80 (nginx, httpd) must be able to bind it, so
// the metadata proxy leaves port 80 in that guest.
func TestMetadataProxyPortLeavesPort80ToWorkloads(t *testing.T) {
	for _, tc := range []struct {
		name string
		used map[int]bool
		want int
	}{
		{"port 80 free", map[int]bool{8080: true}, 80},
		{"workload serves 80", map[int]bool{80: true}, 8775},
		{"first alternate taken", map[int]bool{80: true, 8775: true}, 8776},
	} {
		if got := metadataProxyPort(tc.used); got != tc.want {
			t.Fatalf("%s: port = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestMetadataWorkloadTCPPorts(t *testing.T) {
	root := fstest.MapFS{
		"etc/faas/app.json":       {Data: []byte(`{"entrypoint":["/app"],"port":80,"ports":[{"name":"udp-53","port":53,"protocol":"udp"},{"name":"tcp-9000","port":9000,"protocol":"tcp"}]}`)},
		"etc/faas/workloads.json": {Data: []byte(`{"main":{"name":"main","port":80},"sidecars":[{"name":"proxy","port":8443}]}`)},
	}
	got := metadataWorkloadTCPPorts(root)
	for _, port := range []int{80, 9000, 8443} {
		if !got[port] {
			t.Fatalf("ports = %v, missing %d", got, port)
		}
	}
	if got[53] {
		t.Fatalf("ports = %v, a UDP listener does not block a TCP bind", got)
	}
	// The serving port defaults to 8080 when the manifest declares none.
	if defaulted := metadataWorkloadTCPPorts(fstest.MapFS{"etc/faas/app.json": {Data: []byte(`{"entrypoint":["/app"]}`)}}); !defaulted[8080] || defaulted[80] {
		t.Fatalf("default ports = %v", defaulted)
	}
}

func TestSetMetadataProxyPortMovesEveryEndpoint(t *testing.T) {
	defer setMetadataProxyPort(metadataDefaultPort)
	setMetadataProxyPort(8775)
	for _, endpoint := range []string{EventPublishEndpoint, metadataEnvEndpoint, metadataSecretReloadAckEndpoint} {
		if !strings.HasPrefix(endpoint, "http://169.254.169.254:8775/") {
			t.Fatalf("endpoint %q did not move to the alternate port", endpoint)
		}
	}
	if metadataListenAddr != "169.254.169.254:8775" {
		t.Fatalf("listen address = %q", metadataListenAddr)
	}
	setMetadataProxyPort(metadataDefaultPort)
	if EventPublishEndpoint != "http://169.254.169.254/v1/events:publish" || metadataListenAddr != "169.254.169.254:80" {
		t.Fatalf("default endpoints = %q %q", EventPublishEndpoint, metadataListenAddr)
	}
}
