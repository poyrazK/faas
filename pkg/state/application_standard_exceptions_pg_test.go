package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgApplicationStandardExceptionLifecycle(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardExceptionLifecycle(t, s)
}
func TestPgApplicationStandardExceptionRefusals(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardExceptionRefusals(t, s)
}
func TestPgApplicationStandardExceptionConcurrent(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardExceptionConcurrent(t, s)
}
func TestPgApplicationStandardExceptionExpiry(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardExceptionExpiry(t, s)
}
