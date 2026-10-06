package githubdgrpc

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gitapproval"
)

const ProtectedBranchProfile = gitapproval.ProtectedBranchProfile

// ProtectedBranchEvidence qualifies a current protection policy. It does not
// prove that a particular merge was reviewed, or grant environment approval.
// The approval transaction must retain this evidence with merge/review
// provenance and the immutable definition digest.
type ProtectedBranchEvidence = gitapproval.PolicyEvidence

// Kept optional for older daemon and customer-path service implementations.
type ProtectedBranchEvidenceService interface {
	GetProtectedBranchEvidence(context.Context, string, int64, int64, string, string, string) (ProtectedBranchEvidence, error)
}
