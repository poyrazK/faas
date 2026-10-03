package state

// ADR-224: account subscriptions are not publicly creatable until the
// account CRUD PR. Seed them directly to exercise the release fan-out.
func seedMemAccountReleaseHook(m *MemStore, accountID string, filter []string, enabled bool) AppWebhook {
	hook := memSampleWebhook(accountID, "")
	hook.ID = newID()
	hook.Scope = AppWebhookScopeAccount
	hook.EventFilter = filter
	hook.Enabled = enabled
	m.mu.Lock()
	m.appWebhooks[hook.ID] = hook
	m.mu.Unlock()
	return hook
}
