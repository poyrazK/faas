package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPublicObjectStorageRootOnTheEdgeStore: the object-storage handlers
// assert the store they get to further interfaces (durable meters, request
// admission, tracked uploads). On 2026-10-21 rc.287's edge store lacked some
// and gatewayd-public refused to start with gateway safety accounting on.
func TestPublicObjectStorageRootOnTheEdgeStore(t *testing.T) {
	registry := &objectstorage.Registry{Accounting: api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := publicObjectStorageRoot(state.NewEdgeStore(nil), registry, func() bool { return true },
		testDomains(t, "", "apps.example"), http.NotFoundHandler(), log)
	if err != nil || handler == nil {
		t.Fatalf("publicObjectStorageRoot on the edge store: handler=%v err=%v", handler, err)
	}
}
