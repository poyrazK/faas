//go:build !no_pg

package state

import "testing"

func TestPgStandardRuntimeReceiptOwnedHistory(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeReceiptOwnedHistory(t, s)
}
func TestPgStandardRuntimeReceiptRestart(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeReceiptRestart(t, s)
}
