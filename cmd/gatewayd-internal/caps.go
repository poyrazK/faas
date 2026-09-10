// cmd/gatewayd-internal/caps.go — DEPLOY-1 / ADR-075 cap
// declaration.
//
// gatewayd-internal is the routing + wake + proxy daemon. When guest service
// discovery is enabled it also binds the tenant bridge's DNS port 53, so the
// unit grants only cap_net_bind_service. Every other filesystem and network
// operation goes through vmmd, schedd, or apid. The TLS edge listener remains
// in gatewayd-public (PR #633 / ADR-070).
package main

import "github.com/onebox-faas/faas/pkg/capdecl"

var capsDecl = capdecl.Declaration{
	Allow: []string{
		"cap_net_bind_service",
	},
	Deny: nil,
}
