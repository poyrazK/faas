package state

// An omitted tenant is the account-owned namespace for older callers.
func workflowIdempotencyTenant(tenantIDs []string) string {
	if len(tenantIDs) == 0 {
		return ""
	}
	return tenantIDs[0]
}
