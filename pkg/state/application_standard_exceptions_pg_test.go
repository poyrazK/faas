package state

import "testing"

func TestPgApplicationStandardExceptionLifecycle(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionLifecycle(t, s)
}
func TestPgApplicationStandardExceptionRefusals(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionRefusals(t, s)
}
func TestPgApplicationStandardExceptionConcurrent(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionConcurrent(t, s)
}
func TestPgApplicationStandardExceptionExpiry(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionExpiry(t, s)
}
