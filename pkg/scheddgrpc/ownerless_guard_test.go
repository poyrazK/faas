// spec: §6.2 — invariant 1 needs one admitting schedd per app.

package scheddgrpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5: the control-plane schedd has no owner node, so its
// gRPC guard admitted every app. With the engine's ownership rule installed it
// refuses apps a compute schedd owns, and still serves its own and unpinned
// apps.
func TestOwnerlessServerRefusesPeerOwnedApps(t *testing.T) {
	resolver := &fakeResolver{
		apps: map[string]state.App{
			"peer":     {ID: "peer", NodeID: "fsn-3"},
			"local":    {ID: "local", NodeID: "default-local"},
			"unpinned": {ID: "unpinned"},
		},
		insts: map[string]state.Instance{"peer-vm": {ID: "peer-vm", AppID: "peer"}, "local-vm": {ID: "local-vm", AppID: "local"}},
	}
	s := New(nil, nil, nil).WithOwner("", resolver).WithAppOwnership(func(app state.App) bool {
		return app.NodeID == "" || app.NodeID == "default-local"
	})
	ctx := context.Background()
	if _, err := s.authorizeApp(ctx, "peer"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("peer-owned app: err = %v, want FailedPrecondition", err)
	}
	if _, err := s.authorizeInstance(ctx, "peer-vm"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("peer-owned instance: err = %v, want FailedPrecondition", err)
	}
	for _, id := range []string{"local", "unpinned"} {
		if _, err := s.authorizeApp(ctx, id); err != nil {
			t.Fatalf("%s: %v, want admitted", id, err)
		}
	}
	if _, err := s.authorizeInstance(ctx, "local-vm"); err != nil {
		t.Fatalf("local instance: %v", err)
	}
	legacy := New(nil, nil, nil).WithOwner("", resolver)
	before := resolver.appCalls
	if _, err := legacy.authorizeApp(ctx, "peer"); err != nil || resolver.appCalls != before {
		t.Fatalf("single-box server without an ownership rule: err=%v store reads=%d, want the legacy pass-through", err, resolver.appCalls)
	}
}
