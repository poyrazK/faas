//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectWorkerReleasePG(t *testing.T) {
	testProjectWorkerRelease(t, func(t *testing.T) state.Store { s, _ := pgStore(t); return s })
}
