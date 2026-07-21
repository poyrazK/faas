// Tests for the imaged daemon entrypoint. The actual VM work needs KVM+root
// (//go:build metal); here we only exercise the override gates (digest-only
// base ref + OCI pull timeout env) without booting pgxpool.

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

// TestOciPullTimeout covers the FAAS_OCI_PULL_TIMEOUT_SECONDS knob —
// valid value honors, invalid/empty/non-positive fall back to
// api.OCIPullTimeoutSeconds (60s).
func TestOciPullTimeout(t *testing.T) {
	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("default ociPullTimeout = %ds, want 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "15")
	if got := ociPullTimeout().Seconds(); got != 15 {
		t.Errorf("override ociPullTimeout = %ds, want 15s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "garbage")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("garbage override ociPullTimeout = %ds, want fallback 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "0")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("zero override ociPullTimeout = %ds, want fallback 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "-5")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("negative override ociPullTimeout = %ds, want fallback 60s", int(got))
	}
}

// TestOverrideGate_DigestPinned covers the success path: a digest-pinned
// reference passes the gate. Mirrors the parsing logic in run() so a
// future refactor of the gate is caught here.
func TestOverrideGate_DigestPinned(t *testing.T) {
	cases := []struct {
		name string
		ref  string
	}{
		{"deploy base digest", "ghcr.io/onebox-faas/runtime-base@sha256:" + sha256hex64()},
		{"builder base digest", "ghcr.io/onebox-faas/builder-base@sha256:" + sha256hex64()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := oci.ParseReference(tc.ref)
			if err != nil {
				t.Fatalf("ParseReference(%q) = %v", tc.ref, err)
			}
			if ref.Digest == "" {
				t.Errorf("override gate must accept %q (digest empty)", tc.ref)
			}
		})
	}
}

// TestOverrideGate_BareTagRejected covers the failure path: a tag-only
// reference must be refused by the gate. The run() function itself
// returns an error, but here we exercise the parsing primitive the
// gate uses so a regression on ParseReference (e.g. accepting a tag
// as if it were a digest) is caught.
func TestOverrideGate_BareTagRejected(t *testing.T) {
	cases := []string{
		"ghcr.io/onebox-faas/builder-base:latest",
		"ghcr.io/onebox-faas/runtime-base:1.2.3",
		"docker.io/library/alpine",
	}
	for _, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			parsed, err := oci.ParseReference(ref)
			if err != nil {
				t.Fatalf("ParseReference(%q) returned error: %v", ref, err)
			}
			if parsed.Digest != "" {
				t.Errorf("override gate must REJECT %q (digest %q non-empty)", ref, parsed.Digest)
			}
		})
	}
}

// sha256hex64 returns a 64-hex-char fake sha256 digest. Used only to
// satisfy the `sha256:<64 hex>` shape that oci.ParseReference requires;
// the bytes themselves are not cryptographically meaningful.
func sha256hex64() string {
	return "0000000000000000000000000000000000000000000000000000000000000000"
}
