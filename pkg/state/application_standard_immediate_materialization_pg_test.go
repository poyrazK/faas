//go:build !no_pg

package state

import "testing"

func TestPgApplicationStandardImmediateClaim(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardImmediateClaim(t, s)
}

func TestPgApplicationStandardImmediateReviewPrecedence(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardImmediateReviewPrecedence(t, s)
}
