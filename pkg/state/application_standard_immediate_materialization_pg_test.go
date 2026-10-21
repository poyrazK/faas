//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgApplicationStandardImmediateClaim(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	standardImmediateClaim(t, s)
}

func TestPgApplicationStandardImmediateReviewPrecedence(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	standardImmediateReviewPrecedence(t, s)
}
