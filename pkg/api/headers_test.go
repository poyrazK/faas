package api

import (
	"net/http"
	"testing"
)

func TestPlatformIdentityApplyGuestHeadersOverridesClaims(t *testing.T) {
	h := http.Header{}
	h.Set(DeploymentIDHeader, "attacker-deployment")
	h.Set("X-Faas-Instance", "attacker-instance")
	h.Set("X-Faas-Unknown", "internal-only")

	PlatformIdentity{
		RequestID:           "req-1",
		AppID:               "app-1",
		DeploymentID:        "dep-1",
		TenantID:            "tenant-1",
		InstanceID:          "instance-1",
		NodeID:              "node-1",
		Region:              "eu-west",
		CommitSHA:           "abc123",
		DeploymentTag:       "canary",
		DeploymentCreatedAt: "2026-09-19T12:00:00Z",
		ImageDigest:         "sha256:deadbeef",
	}.ApplyGuestHeaders(h)

	for name, want := range map[string]string{
		RequestIDHeader:           "req-1",
		AppIDHeader:               "app-1",
		DeploymentIDHeader:        "dep-1",
		TenantIDHeader:            "tenant-1",
		InstanceIDHeader:          "instance-1",
		NodeIDHeader:              "node-1",
		RegionHeader:              "eu-west",
		CommitSHAHeader:           "abc123",
		DeploymentTagHeader:       "canary",
		DeploymentCreatedAtHeader: "2026-09-19T12:00:00Z",
		ImageDigestHeader:         "sha256:deadbeef",
		"X-Faas-App":              "app-1",
		"X-Faas-Instance":         "instance-1",
		"X-Faas-Node":             "node-1",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestClearGuestIdentityHeadersRemovesCanonicalAndLegacy(t *testing.T) {
	h := http.Header{}
	h.Set(RequestIDHeader, "req")
	h.Set(DeploymentIDHeader, "dep")
	h.Set("X-Faas-App", "app")
	h.Set("X-Faas-Instance", "instance")
	h.Set("X-Faas-Node", "node")
	h.Set("X-Faas-Invocation-Id", "invoke")

	ClearGuestIdentityHeaders(h)

	for _, name := range []string{RequestIDHeader, DeploymentIDHeader, "X-Faas-App", "X-Faas-Instance", "X-Faas-Node"} {
		if got := h.Get(name); got != "" {
			t.Errorf("%s survived clear: %q", name, got)
		}
	}
	if got := h.Get(InvocationIDHeader); got != "invoke" {
		t.Errorf("invocation id = %q, want preserved value", got)
	}
}

func TestPlatformIdentityEnvKeyIncludesImageDigest(t *testing.T) {
	if !IsPlatformIdentityEnvKey(PlatformImageDigestEnv) {
		t.Fatal("image digest env key is not reserved")
	}
	if !IsGuestIdentityHeader(ImageDigestHeader) {
		t.Fatal("image digest header is not guest-allowlisted")
	}
}
