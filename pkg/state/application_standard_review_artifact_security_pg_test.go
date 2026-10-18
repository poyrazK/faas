//go:build !no_pg

package state

import "testing"

func TestPgApplicationStandardArtifactSecurityReview(t *testing.T) {
	standardArtifactSecurityReview(t, func(t *testing.T) standardArtifactReviewTestStore {
		s, _ := registryVerificationPGStore(t)
		return s
	})
}
func TestPgApplicationStandardArtifactSecurityBlockers(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	standardArtifactSecurityBlockers(t, s)
}
func TestPgApplicationStandardArtifactReviewRescanInvalidatesApproval(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	standardArtifactReviewRescan(t, s)
}
