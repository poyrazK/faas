package main

// ADR-570: compute-owned platform responses (app and job task log reads and
// streams, attach sessions, the dev debug tunnel) reach the public hop
// through its data proxy, which refuses a successful response that carries no
// security snapshot. They are enrolled under their owner's scopes, so an
// account or app revocation also ends them.

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// admitPlatformTraffic enrolls a platform response under its owner's account
// and, when appID is set, app, and stamps the snapshot into header. It
// returns the context the response must run on, which ends when a scope is
// revoked, or ok=false after writing the refusal. release ends the
// enrollment once the response is complete.
func admitPlatformTraffic(ctx context.Context, w http.ResponseWriter, r *http.Request, registry *trafficrevocation.Registry, header http.Header, accountID, appID string) (context.Context, func(), bool) {
	scopes := []trafficrevocation.Scope{{Kind: "account", ID: accountID}}
	if appID != "" {
		scopes = append(scopes, trafficrevocation.Scope{Kind: "app", ID: appID})
	}
	return gateway.AdmitPlatformResponse(ctx, w, r, registry, header, scopes...)
}
