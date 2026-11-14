package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261110210000 — slice 3j-3: each private registry pulls from its
// loopback mirror over plain HTTP before the registry itself; Docker Hub
// lists a private mirror ahead of the operator's mirrors.
func TestBuildkitRegistryConfigWithPrivateMirrors(t *testing.T) {
	private := api.PlanBuildRegistryMirrors([]string{"ghcr.io", "registry-1.docker.io"})
	got, err := buildkitRegistryConfig([]string{"mirror.gcr.io"}, private)
	if err != nil {
		t.Fatal(err)
	}
	want := "[registry.\"ghcr.io\"]\n  mirrors = [\"127.0.0.1:15000\"]\n" +
		"[registry.\"docker.io\"]\n  mirrors = [\"127.0.0.1:15001\", \"mirror.gcr.io\"]\n" +
		"[registry.\"127.0.0.1:15000\"]\n  http = true\n" +
		"[registry.\"127.0.0.1:15001\"]\n  http = true\n"
	if got != want {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
	// A plan that does not match the ports, or names a host that would
	// break out of the TOML string, is refused.
	bad := []api.BuildRegistryMirror{{Registry: `a.io"] http = true #`, GuestPort: 15000, VsockPort: 1100}}
	if _, err := buildkitRegistryConfig(nil, bad); err == nil {
		t.Fatal("a registry that breaks out of the TOML string was accepted")
	}
	if _, err := buildkitRegistryConfig(nil, []api.BuildRegistryMirror{{Registry: "a.io", GuestPort: 22, VsockPort: 1100}}); err == nil {
		t.Fatal("a mirror on an unplanned port was accepted")
	}
}

func TestBuildkitMirrorConfig(t *testing.T) {
	got, err := buildkitMirrorConfig([]string{"mirror.gcr.io", "cache.internal:5000"})
	if err != nil {
		t.Fatal(err)
	}
	want := "[registry.\"docker.io\"]\n  mirrors = [\"mirror.gcr.io\", \"cache.internal:5000\"]\n"
	if got != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
	// A host is written into TOML, so anything but a plain host is refused.
	if _, err := buildkitMirrorConfig([]string{`mirror"] insecure = true #`}); err == nil {
		t.Fatal("a host that breaks out of the TOML string was accepted")
	}
}
