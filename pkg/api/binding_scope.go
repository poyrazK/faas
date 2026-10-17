package api

// QueueBindingInScope includes shared legacy bindings and bindings belonging to
// the selected environment. The explicit environment distinguishes a project
// environment named "app" from the legacy inventory's app-wide scope label.
// Older inventories do not contain QueueEnvironment and retain their scope label.
func QueueBindingInScope(item AppBindingInventoryItem, scope string) bool {
	if item.Type != BindingTypeQueue {
		return false
	}
	if item.QueueEnvironment != nil {
		return *item.QueueEnvironment == "" || *item.QueueEnvironment == scope
	}
	return item.Scope == "app" || item.Scope == scope
}
