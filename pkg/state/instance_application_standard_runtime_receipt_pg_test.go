//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgStandardRuntimeReceiptOwnedHistory(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardRuntimeReceiptOwnedHistory(t, s)
}
func TestPgStandardRuntimeReceiptRestart(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardRuntimeReceiptRestart(t, s)
}
