package githubd

// Capability flags (ADR-20261023115741): githubd stops the GitHub work of a
// capability whose platform flag is off for the bound account. A push or pull
// request event for such an account is acknowledged and ignored; it is not
// deployed later when the flag comes back on.
const (
	// CapabilityGitHubDeploys gates push-to-deploy.
	CapabilityGitHubDeploys = "github-deploys"
	// CapabilityPRPreviews gates pull-request preview apps. Previews also
	// need GitHub deploys.
	CapabilityPRPreviews = "pr-previews"
)

// capabilityAllowed decides a capability for an account. A nil decider (unit
// tests and embedded services) releases every capability.
func (s *Service) capabilityAllowed(capability, accountID string) bool {
	return s.CapabilityAllowed == nil || s.CapabilityAllowed(capability, accountID)
}

// previewsAllowed reports whether an account may get pull-request previews.
func (s *Service) previewsAllowed(accountID string) bool {
	return s.capabilityAllowed(CapabilityGitHubDeploys, accountID) && s.capabilityAllowed(CapabilityPRPreviews, accountID)
}
