//go:build !no_pg

// adr: 569
package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgInvocationEnvironmentRoutingContract(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := testInvocationEnvironmentRouting(t, store)
	request := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke}
	request.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: f.stageRelease.ID})
	if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at = $1 WHERE id = $2`, time.Now().Add(-time.Hour), f.stageRelease.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired stage release fell back: %v", err)
	}
	// A malformed graph must not wake a production member under a stage scope.
	activeID, _, err := store.ResolveProjectRelease(ctx, f.app.ID, "staging", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_release_members SET deployment_id = $1 WHERE release_id = $2 AND app_id = $3`, f.production.ID, activeID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	request.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: activeID})
	if _, _, err := state.ResolveInvocationVersion(ctx, store, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stage graph reached production: %v", err)
	}
}
