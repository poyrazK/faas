package githubd

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

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

// scanOptions are the capability-gated scan options for an account. apid
// scans a reviewed deploy with the same options, so a push deploy produces
// the workloads the account reviewed.
func (s *Service) scanOptions(accountID string) reposcan.ScanOptions {
	return reposcan.ScanOptions{
		KubernetesManifests:       s.capabilityAllowed(reposcan.KubernetesManifestsCapability, accountID),
		HTTPSTransportUnavailable: !s.capabilityAllowed(api.PrivateServiceHTTPSCapability, accountID),
	}
}

// previewsAllowed reports whether an account may get pull-request previews.
func (s *Service) previewsAllowed(accountID string) bool {
	return s.capabilityAllowed(CapabilityGitHubDeploys, accountID) && s.capabilityAllowed(CapabilityPRPreviews, accountID)
}
