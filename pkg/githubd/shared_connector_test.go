package githubd

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/githubscope"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestRealServiceDisconnectedAccountCannotUseWarmInstallation(t *testing.T) {
	svc := newTestRealService(t, "first")
	svc.Tokens = NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) { return "warm", time.Now().Add(time.Hour), nil }), time.Minute)
	if _, _, err := svc.ensureInstallTokenForInstallation(t.Context(), "first", 1); err != nil {
		t.Fatal(err)
	}
	store := svc.Installs.(*memStoreInstalls)
	inst, err := store.ForAccount(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	inst.AccountID = "second"
	if err := store.Upsert(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	delete(store.items, "first\x001")
	store.mu.Unlock()
	if _, _, err := svc.ensureInstallTokenForInstallation(t.Context(), "first", 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disconnected cached token authorized: %v", err)
	}
	st, _, _, err := svc.GetInstallState("first")
	if err != nil || st != githubdgrpc.InstallStateUnspecified {
		t.Fatalf("stale state: %v %v", st, err)
	}
	if _, token, err := svc.ensureInstallTokenForInstallation(t.Context(), "second", 1); err != nil || token == "" {
		t.Fatalf("other account disconnected: %v", err)
	}
}
func TestSharedAccountCheckIdentity(t *testing.T) {
	checks, body := newPreviewChecksAPI(t)
	var names, external []string
	for _, account := range []string{"first", "second", "first"} {
		ctx := githubscope.WithAccount(context.Background(), account)
		if err := checks.WritePreviewCheck(ctx, "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "", ""); err != nil {
			t.Fatal(err)
		}
		names = append(names, (*body.Load())["name"].(string))
		external = append(external, (*body.Load())["external_id"].(string))
	}
	if names[0] == names[1] || external[0] == external[1] || names[0] != names[2] || external[0] != external[2] {
		t.Fatalf("account identities collide or change: %v %v", names, external)
	}
}
