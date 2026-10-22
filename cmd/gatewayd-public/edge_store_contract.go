package main

import (
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Every store interface this daemon, or a handler it builds, asserts at run
// time on its state.EdgeStore. A type assertion is not a compile-time check:
// on 2026-10-21 rc.287's gatewayd-public lacked methods of the object-storage
// accounting and egress stores, the assertions in main.go and
// objectstorage.NewPublicReadHandler came back false, and the daemon refused
// to start. Listing them here makes a missing method fail the build
// (docs/adr/20261021092138-edge-store-blast-radius.md). Add the interface
// when you add an assertion on the store.
var (
	// main.go
	_ state.ObjectStorageProviderUsageStore = (*state.EdgeStore)(nil)
	_ state.ObjectStorageAccountingStore    = (*state.EdgeStore)(nil)
	_ state.ObjectUploadRouteStore          = (*state.EdgeStore)(nil)
	_ state.ObjectBucketStore               = (*state.EdgeStore)(nil)
	_ objectstorage.UploadAuthenticator     = (*state.EdgeStore)(nil)
	// runtime_public_edges.go
	_ state.RuntimeUpgradePublicEdgeWithdrawalStore = (*state.EdgeStore)(nil)
	_ state.RuntimeUpgradePublicEdgeActivityStore   = (*state.EdgeStore)(nil)
	// pkg/objectstorage public_read.go and upload*.go
	_ state.ObjectStorageGatewayEgressStore  = (*state.EdgeStore)(nil)
	_ state.ObjectStorageGatewayRequestStore = (*state.EdgeStore)(nil)
	_ state.ObjectBucketMutationStore        = (*state.EdgeStore)(nil)
	_ state.ObjectTrackedUploadMutationStore = (*state.EdgeStore)(nil)
	_ state.ObjectTrackedUploadStore         = (*state.EdgeStore)(nil)
	_ state.ObjectBucketObjectLockStore      = (*state.EdgeStore)(nil)
)
