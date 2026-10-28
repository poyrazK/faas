package gateway

func (a App) rateOwnerID() string {
	if a.ResourceOwnerID != "" {
		return a.ResourceOwnerID
	}
	return a.AccountID
}

// edgeRuleOwnerID is the tenant key a host's edge-rule set is scoped by: the
// app's org (its personal org for a personal app), else its account. It mirrors
// the store's COALESCE(apps.org_id, apps.account_id) so a rule written on
// another tenant's app — another org, or the same creator's personal account,
// even after they left the org — never applies to this host (Hunt 24 H24-1).
// It deliberately differs from rateOwnerID, which drops to the account for a
// company that has not enabled resource-owned billing.
func (a App) edgeRuleOwnerID() string {
	if a.OrgID != "" {
		return a.OrgID
	}
	return a.AccountID
}
