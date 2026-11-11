package main

import (
	"errors"
	"testing"
)

// A DNS or registry failure while BuildKit pulled Gregale's own images was
// recorded as the customer's build error (bug hunt H105). It is now
// infrastructure; a customer's own image or step failing stays a user error.
func TestPlatformPullFailureIsInfrastructure(t *testing.T) {
	const runtimeBase = "registry.gregale.dev/runtime/node22:1.4@sha256:abc"
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"railpack frontend dns", `ERROR: failed to solve: ghcr.io/railwayapp/railpack-frontend:v0.38.0: failed to resolve source metadata: dial tcp: lookup ghcr.io: no such host`, true},
		{"runtime base timeout", `failed to do request: Head "https://registry.gregale.dev/v2/runtime/node22/manifests/1.4": dial tcp 10.1.2.3:443: i/o timeout`, true},
		{"customer base image missing", `failed to resolve source metadata for docker.io/library/nodee:22: not found`, false},
		{"customer image network", `failed to do request: Head "https://docker.io/v2/acme/app": i/o timeout`, false},
		{"build step failed", `npm ERR! missing script: build`, false},
	}
	for _, tc := range cases {
		if got := platformPullFailure([]byte(tc.output), runtimeBase); got != tc.want {
			t.Errorf("%s: platformPullFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
	code, class := buildExitStatus(builderNetworkError{errors.New("pull platform image")})
	if code != 1 || class != "FailureInfra" {
		t.Fatalf("buildExitStatus = %d, %q; want 1, FailureInfra", code, class)
	}
}
