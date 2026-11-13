// adr: 20261104143000
package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func declareUDPListener(t *testing.T, store interface {
	UpdateApp(context.Context, string, state.UpdateAppParams) (state.App, error)
}, appID, name string, port int) {
	t.Helper()
	manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: name, Port: port, Protocol: api.WorkloadPortUDP}}}
	if _, err := store.UpdateApp(t.Context(), appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
}
