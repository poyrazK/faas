//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgApplicationStandardArtifactSecurityReview(t *testing.T) {
	standardArtifactSecurityReview(t, func(t *testing.T) standardArtifactReviewTestStore {
		s, _ := state.RegistryVerificationPGStore(t)
		return s
	})
}
func TestPgApplicationStandardArtifactSecurityBlockers(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	standardArtifactSecurityBlockers(t, s)
}
func TestPgApplicationStandardArtifactReviewRescanInvalidatesApproval(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	standardArtifactReviewRescan(t, s)
}
