package main

import "testing"

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
