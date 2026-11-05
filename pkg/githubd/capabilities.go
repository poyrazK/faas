package githubd

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
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

// capabilityAllowed decides a capability for a flag subject. A nil decider
// (unit tests and embedded services) releases every capability.
func (s *Service) capabilityAllowed(capability string, subject platformflags.Subject) bool {
	return s.CapabilityAllowed == nil || s.CapabilityAllowed(capability, subject)
}

// capabilitySubject is the flag subject of GitHub work for an app or project:
// the bound account, the organization that owns the resource and the plan
// that applies to it, as apid evaluates the same flags (ADR-20261105090226).
// Organization and plan rules were ignored when githubd passed the account
// alone. A plan that cannot be read leaves the plan out, so plan rules do
// not match rather than the event failing.
func (s *Service) capabilitySubject(ctx context.Context, accountID, orgID string) platformflags.Subject {
	subject := platformflags.Subject{AccountID: accountID, OrgID: orgID}
	if s.Reconcile == nil || s.Reconcile.Store == nil || accountID == "" {
		return subject
	}
	owners, ok := s.Reconcile.Store.(state.AppOwnerReader)
	if !ok {
		return subject
	}
	if owner, err := state.AppResourceAccount(ctx, owners, state.App{AccountID: accountID, OrgID: orgID}); err == nil {
		subject.Plan = string(owner.Plan)
	}
	return subject
}

// scanOptions are the capability-gated scan options for a subject. apid
// scans a reviewed deploy with the same options, so a push deploy produces
// the workloads the account reviewed.
func (s *Service) scanOptions(subject platformflags.Subject) reposcan.ScanOptions {
	return reposcan.ScanOptions{
		KubernetesManifests:       s.capabilityAllowed(reposcan.KubernetesManifestsCapability, subject),
		HTTPSTransportUnavailable: !s.capabilityAllowed(api.PrivateServiceHTTPSCapability, subject),
		ResourceLabels:            s.capabilityAllowed(labels.Capability, subject),
	}
}

// previewsAllowed reports whether a subject may get pull-request previews.
func (s *Service) previewsAllowed(subject platformflags.Subject) bool {
	return s.capabilityAllowed(CapabilityGitHubDeploys, subject) && s.capabilityAllowed(CapabilityPRPreviews, subject)
}
