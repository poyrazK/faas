// cmd/schedd/caps.go — DEPLOY-1 / ADR-075 cap declaration.
//
// schedd is the engine: it owns the instance state machine
// (spec §6) and routes wake/park/cron events to vmmd / imaged.
// VM and network mutation goes through vmmd's gRPC. The one narrow local
// exception is read-only conntrack enumeration for idle-flow accounting;
// conntrack's netlink socket requires CAP_NET_ADMIN on Linux even for reads.
package main

import "github.com/onebox-faas/faas/pkg/capdecl"

var capsDecl = capdecl.Declaration{
	Allow: []string{"cap_net_admin"},
	Deny:  nil,
}
