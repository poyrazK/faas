package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestMemStoreConformance(t *testing.T) {
	conformance.Run(t, func(_ *testing.T) state.Store {
		return state.NewMemStore()
	})
}
