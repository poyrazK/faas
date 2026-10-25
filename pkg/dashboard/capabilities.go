package dashboard

import "sync/atomic"

// capabilityDecider is installed once by apid's wiring (ADR-20261023115741);
// unset, every capability counts as released.
var capabilityDecider atomic.Pointer[func(accountID, plan, capability string) bool]

// SetCapabilityDecider installs the function pages use to hide links to
// capabilities whose platform flag is off for the signed-in account.
func SetCapabilityDecider(decide func(accountID, plan, capability string) bool) {
	capabilityDecider.Store(&decide)
}

// On reports whether a capability is released to the page's account.
// Templates guard links with it ({{if $.On "realtime"}}) so a capability
// that is off leaves no link to a page that would answer 404.
func (p Page) On(capability string) bool {
	decide := capabilityDecider.Load()
	if decide == nil || p.Account == nil {
		return true
	}
	return (*decide)(p.Account.ID, p.Account.Plan, capability)
}
