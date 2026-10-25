// state_enqueue.go — wires githubd's push-dispatch seam to the apid gRPC
// bridge (issue #432 phase 5).
//
// The enqueuer itself (BuildSpec → EnqueueBuildRequest) lives in
// pkg/githubd/apid_enqueuer.go so the webhook-to-deployment acceptance test
// drives the production mapping. This wrapper keeps the cmd-side stub
// fallback for a nil client: the dispatcher then logs and skips with
// errApidBridgeNotReady instead of crashing.
package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/githubd"
)

// NewApidEnqueuer returns a githubd.BuildEnqueuer that passes each BuildSpec
// to the apid gRPC bridge. The client is the ApidBridgeClient returned by
// newApidBridgeClient (stub or live depending on
// FAAS_APID_GITHUBD_BRIDGE_SOCK). nil client falls back to the stub.
func NewApidEnqueuer(client ApidBridgeClient, log *slog.Logger) githubd.BuildEnqueuer {
	if client == nil {
		client = stubApidBridgeClient{}
	}
	return githubd.NewApidEnqueuer(client, log)
}
