package previewdb

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestCopiedStateSkipsBindingsProductionCannotUse(t *testing.T) {
	for _, tc := range []struct {
		state managedpostgres.BindingState
		want  bool
	}{
		{managedpostgres.BindingStateReady, true},
		{managedpostgres.BindingStateProvisioning, true},
		{managedpostgres.BindingStateFailed, false},
		{managedpostgres.BindingStateRetiring, false},
		{managedpostgres.BindingStateDeleting, false},
		{managedpostgres.BindingStateDeleted, false},
	} {
		if got := copiedState(tc.state); got != tc.want {
			t.Errorf("copiedState(%s) = %v, want %v", tc.state, got, tc.want)
		}
	}
}
