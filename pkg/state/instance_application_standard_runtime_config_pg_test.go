//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgApplicationStandardRuntimeConfigPublication(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	state.StandardRuntimeConfigPublication(t, s)
}
