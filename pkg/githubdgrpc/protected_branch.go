package githubdgrpc

import (
	"context"
	"regexp"
	"time"
)

const ProtectedBranchProfile = "classic_reviewed_branch/v1"

// ProtectedBranchEvidence qualifies a current protection policy. It does not
// prove that a particular merge was reviewed, or grant environment approval.
// The future approval transaction must retain this evidence with merge/review
// provenance and the immutable definition digest.
type ProtectedBranchEvidence struct {
	Qualified      bool
	Reason         string
	Profile        string
	InstallationID int64
	RepositoryID   int64
	Repository     string
	Branch         string
	CommitSHA      string
	PolicyDigest   string
	CheckedAt      time.Time
}

// Kept optional for older daemon and customer-path service implementations.
type ProtectedBranchEvidenceService interface {
	GetProtectedBranchEvidence(context.Context, string, int64, int64, string, string, string) (ProtectedBranchEvidence, error)
}

var protectedBranchSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var protectedBranchDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (e ProtectedBranchEvidence) ValidFor(installationID, repositoryID int64, repository, branch, commitSHA string, now time.Time, maxAge time.Duration) bool {
	return e.Qualified && e.Reason == "" && e.Profile == ProtectedBranchProfile &&
		installationID > 0 && repositoryID > 0 && e.InstallationID == installationID && e.RepositoryID == repositoryID &&
		e.Repository == repository && e.Branch == branch && e.CommitSHA == commitSHA &&
		protectedBranchSHA.MatchString(commitSHA) && protectedBranchDigest.MatchString(e.PolicyDigest) &&
		!e.CheckedAt.IsZero() && !now.IsZero() && maxAge > 0 && !e.CheckedAt.After(now) && now.Sub(e.CheckedAt) <= maxAge
}
