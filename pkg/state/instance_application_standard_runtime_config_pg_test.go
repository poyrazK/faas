//go:build !no_pg

package state

import "testing"

func TestPgApplicationStandardRuntimeConfigPublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeConfigPublication(t, s)
}
