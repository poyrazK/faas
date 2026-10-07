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
		{"POST", "/v1/platform-tenant-self/apps/my-app/operations", true},
		{"GET", "/v1/platform-tenant-self/apps/my-app/operations", false},
		{"POST", "/v1/platform-tenant-self/apps//operations", false},
		{"POST", "/v1/platform-tenant-self/apps/my-app/workflows/process-order/runs", true},
		{"GET", "/v1/platform-tenant-self/apps/my-app/workflows/process-order/runs", false},
		{"POST", "/v1/platform-tenant-self/apps/my-app/workflows//runs", false},
		{"POST", "/v1/platform-tenant-self/apps/my-app/workflows/process-order/runs/extra", false},
		{"GET", "/v1/platform-tenant-self/operations/id", true},
		{"POST", "/v1/platform-tenant-self/operations/id/cancel", true},
		{"POST", "/v1/platform-tenant-self/operations/id", false},
		{"GET", "/v1/platform-tenant-self/operations/id/cancel", false},
		// ADR-521: only the staged HTTP customer namespace is reachable.
		{"POST", "/v1/platform-tenant-self/customer-operations", true},
		{"POST", "/v1/platform-tenant-self/customer-operations/submissions/lookup", true},
		{"GET", "/v1/platform-tenant-self/customer-operations/submissions/lookup", false},
		{"DELETE", "/v1/platform-tenant-self/customer-operations/submissions/lookup", false},
		{"POST", "/v1/platform-tenant-self/customer-operations/submissions/lookup/extra", false},
		{"POST", "/v1/platform-tenant-self/customer-operations/submissions/other", false},
		{"GET", "/v1/platform-tenant-self/customer-operations", true},
		{"GET", "/v1/platform-tenant-self/customer-operations/id", true},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/events", true},
		{"POST", "/v1/platform-tenant-self/customer-operations/id/cancel", true},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/artifacts/file", true},
		{"POST", "/v1/platform-tenant-self/customer-operations/id/artifacts/file", false},
		{"DELETE", "/v1/platform-tenant-self/customer-operations/id/artifacts/file", false},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/artifacts/", false},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/artifacts/file/extra", false},
		{"POST", "/v1/platform-tenant-self/customer-operations/id/recover", false},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/cancel", false},
		{"GET", "/v1/platform-tenant-self/customer-operations//events", false},
		{"GET", "/v1/platform-tenant-self/customer-operations/id/events/extra", false},
		{"GET", "/v1/platform-tenant-self/workflows/runs/id", true},
		{"POST", "/v1/platform-tenant-self/workflows/runs/id/cancel", true},
		{"POST", "/v1/platform-tenant-self/workflows/runs/id/resume", true},
		{"DELETE", "/v1/platform-tenant-self/workflows/runs/id/cancel", false},
		{"GET", "/v1/platform-tenant-self/workflows/runs/id/resume", false},
		{"POST", "/v1/platform-tenant-self/workflows/runs//cancel", false},
		{"POST", "/v1/platform-tenant-self/workflows/runs/id/cancel/extra", false},
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
