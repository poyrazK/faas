//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectReleaseConfigurationPG(t *testing.T) {
	testProjectReleaseConfiguration(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}
