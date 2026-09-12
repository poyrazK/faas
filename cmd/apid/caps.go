// cmd/apid/caps.go — DEPLOY-1 / ADR-075 cap declaration.
//
// apid is the customer-facing API daemon. It serves a Unix socket and high
// loopback ports behind Caddy, so it needs no Linux capabilities. The empty
// declaration matches the empty bounding and ambient sets in the systemd unit.
//
// apid is NOT a root component — it runs as User=faas-apid
// with NoNewPrivileges=yes. The systemd bounding set enforces the empty set;
// the runtimecheck ensures apid never starts requiring an undeployed cap.
//
// A future PR that adds a real privilege requirement to apid
// (e.g. snapshot-aware request routing) MUST extend this
// declaration and the systemd bounding/ambient sets, and cite the ADR. The
// lint rule blocks pkg/vmmdgrpc
// imports outside cmd/vmmd/ + pkg/vmmd/ so a misuse attempt
// trips at CI time.
package main

import "github.com/onebox-faas/faas/pkg/capdecl"

var capsDecl = capdecl.Declaration{
	Allow: nil,
	Deny:  nil,
}
