package middleware

import "testing"

func TestPlatformTenantInvocationPathsAllowed(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/v1/platform-tenant-self/invocations/id", true},
		{"POST", "/v1/platform-tenant-self/invocations/id/cancel", true},
		{"POST", "/v1/platform-tenant-self/invocations/id/replay", true},
		{"POST", "/v1/platform-tenant-self/invocations/id", false},
		{"GET", "/v1/platform-tenant-self/invocations/id/replay", false},
		{"DELETE", "/v1/platform-tenant-self/invocations/id/cancel", false},
		{"GET", "/v1/platform-tenant-self/invocations/", false},
		{"POST", "/v1/platform-tenant-self/invocations//cancel", false},
		{"POST", "/v1/platform-tenant-self/invocations/id/replay/extra", false},
		{"GET", "/v1/invocations/id", false},
		{"POST", "/v1/account/platform-tenants/id/invocations/id/replay", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			if got := platformTenantSelfPathAllowed(tc.method, tc.path); got != tc.allowed {
				t.Fatalf("allowed=%t want=%t", got, tc.allowed)
			}
		})
	}
}
