//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestDeploymentConfigurationPG(t *testing.T) {
	testDeploymentConfiguration(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}
