package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestPlatformIdentityApplyGuestHeadersOverridesClaims(t *testing.T) {
	h := http.Header{}
	h.Set(DeploymentIDHeader, "attacker-deployment")
	h.Set("X-Faas-Instance", "attacker-instance")
	h.Set("X-Faas-Unknown", "internal-only")
	h.Set(TargetDeploymentHeader, "attacker-deployment")
	h.Set(PlatformTenantIDHeader, "attacker-tenant")
	h.Set(ManagedOperationResultVersionHeader, "attacker-version")
	h.Set(OperationReceiptVersionHeader, "attacker-version")
	h.Set(OperationReceiptBindingHeader, "attacker-binding")
	h.Set(OperationTransactionVersionHeader, "1")
	h.Set(OperationMilestoneVersionHeader, "1")
	h.Set(OperationResultMaxBytesHeader, "1048576")

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
	if got := h.Get(TargetDeploymentHeader); got != "" {
		t.Fatalf("public override header survived gateway boundary: %q", got)
	}

	for name, want := range map[string]string{
		RequestIDHeader:                     "req-1",
		AppIDHeader:                         "app-1",
		DeploymentIDHeader:                  "dep-1",
		TenantIDHeader:                      "tenant-1",
		PlatformTenantIDHeader:              "",
		ManagedOperationResultVersionHeader: "",
		OperationReceiptVersionHeader:       "",
		OperationReceiptBindingHeader:       "",
		OperationTransactionVersionHeader:   "",
		OperationMilestoneVersionHeader:     "",
		OperationResultMaxBytesHeader:       "",
		InstanceIDHeader:                    "instance-1",
		NodeIDHeader:                        "node-1",
		RegionHeader:                        "eu-west",
		CommitSHAHeader:                     "abc123",
		DeploymentTagHeader:                 "canary",
		DeploymentCreatedAtHeader:           "2026-09-19T12:00:00Z",
		ImageDigestHeader:                   "sha256:deadbeef",
		"X-Faas-App":                        "app-1",
		"X-Faas-Instance":                   "instance-1",
		"X-Faas-Node":                       "node-1",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestPlatformIdentityPlatformTenantClaimIsAuthoredOnlyWhenVerified(t *testing.T) {
	h := http.Header{}
	h.Set(PlatformTenantIDHeader, "forged")
	(PlatformIdentity{TenantID: "account-1", PlatformTenantID: "customer-1"}).ApplyGuestHeaders(h)
	if got := h.Get(PlatformTenantIDHeader); got != "customer-1" {
		t.Fatalf("verified platform tenant = %q", got)
	}
	(PlatformIdentity{TenantID: "account-1"}).ApplyGuestHeaders(h)
	if got := h.Get(PlatformTenantIDHeader); got != "" {
		t.Fatalf("anonymous request inherited tenant claim %q", got)
	}
	if got := h.Get(TenantIDHeader); got != "account-1" {
		t.Fatalf("account tenant header changed: %q", got)
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
	h.Set(FlagContextHeader, "customer-authored")
	h.Set(TargetDeploymentHeader, "dep")

	ClearGuestIdentityHeaders(h)

	for _, name := range []string{RequestIDHeader, DeploymentIDHeader, "X-Faas-App", "X-Faas-Instance", "X-Faas-Node", TargetDeploymentHeader, FlagContextHeader} {
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
	if !IsGuestIdentityHeader(FlagContextHeader) {
		t.Fatal("flag context header is not reserved at the guest boundary")
	}
}

func TestPlatformIdentityApplyGuestHeadersOverridesClaimsIntegratedBusinessWorkflows(t *testing.T) {
	h := http.Header{}
	h.Set(DeploymentIDHeader, "attacker-deployment")
	h.Set("X-Faas-Instance", "attacker-instance")
	h.Set("X-Faas-Unknown", "internal-only")
	h.Set(TargetDeploymentHeader, "attacker-deployment")
	h.Set(PlatformTenantIDHeader, "attacker-tenant")
	h.Set(ManagedOperationResultVersionHeader, "attacker-version")
	h.Set(OperationTransactionVersionHeader, "1")
	h.Set(OperationMilestoneVersionHeader, "1")
	h.Set(OperationResultMaxBytesHeader, "1048576")

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
	if got := h.Get(TargetDeploymentHeader); got != "" {
		t.Fatalf("public override header survived gateway boundary: %q", got)
	}

	for name, want := range map[string]string{
		RequestIDHeader:                     "req-1",
		AppIDHeader:                         "app-1",
		DeploymentIDHeader:                  "dep-1",
		TenantIDHeader:                      "tenant-1",
		PlatformTenantIDHeader:              "",
		ManagedOperationResultVersionHeader: "",
		OperationTransactionVersionHeader:   "",
		OperationMilestoneVersionHeader:     "",
		OperationResultMaxBytesHeader:       "",
		InstanceIDHeader:                    "instance-1",
		NodeIDHeader:                        "node-1",
		RegionHeader:                        "eu-west",
		CommitSHAHeader:                     "abc123",
		DeploymentTagHeader:                 "canary",
		DeploymentCreatedAtHeader:           "2026-09-19T12:00:00Z",
		ImageDigestHeader:                   "sha256:deadbeef",
		"X-Faas-App":                        "app-1",
		"X-Faas-Instance":                   "instance-1",
		"X-Faas-Node":                       "node-1",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// HasLowerPrefix replaces strings.HasPrefix(strings.ToLower(name), prefix) on
// the request path, so it must answer exactly as that did, including for
// names whose non-ASCII runes lower to ASCII (U+0130 lowers to "i").
func TestHasLowerPrefixMatchesToLower(t *testing.T) {
	prefixes := []string{"x-faas-", "x-gregale-operation-", "x-gregale-customer-operation-", "x-gregale-event-", "x-"}
	names := []string{
		"", "x", "X-", "x-faas", "X-Faas-Request-Id", "x-faas-app", "X-FAAS-INSTANCE", "Accept-Encoding",
		"X-Gregale-Operation-Id", "x-gregale-customer-operation-id", "X-Gregale-Event-Type", "X-Faa-S",
		"X-Gregale-Operat\u0130on-Id", "\u212a-faas-x", "x-faa\u017f-x", "X-Faas-\xff", "\xff-faas-",
	}
	for _, name := range names {
		for _, prefix := range prefixes {
			want := strings.HasPrefix(strings.ToLower(name), prefix)
			if got := HasLowerPrefix(name, prefix); got != want {
				t.Errorf("HasLowerPrefix(%q, %q) = %v, want %v", name, prefix, got, want)
			}
		}
	}
}

func BenchmarkHeaderClassification(b *testing.B) {
	names := []string{"Accept", "Accept-Encoding", "User-Agent", "Cf-Ray", "Cf-Connecting-Ip", "X-Forwarded-For",
		"X-Forwarded-Proto", "Traceparent", "X-Faas-Request-Id", "X-Gregale-Operation-Id"}
	b.ReportAllocs()
	for b.Loop() {
		for _, name := range names {
			_ = IsGuestIdentityHeader(name)
		}
	}
}
