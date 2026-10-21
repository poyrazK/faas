package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/wire"
)

// handoffChecks are the requests this generation sends its successor before
// the successor takes traffic (docs/adr/20261020214632-automatic-unit-deploy-
// safety-rails.md, section 4): its readiness on the control listener and, on
// the control plane, one unauthenticated API request on the public listener,
// which the successor proxies to apid (control_plane_proxy.go). apid answers
// 401, or 429 once loopback has spent its auth-failure budget; either proves
// the routing, the proxy hop and apid's auth chain. A compute edge serves app
// hosts only and checks readiness only.
func handoffChecks(publicAddr, controlAddr string, computeEdge bool) []wire.HandoffCheck {
	checks := []wire.HandoffCheck{{
		Name: "readyz", Network: "tcp", Address: controlAddr, Path: "/readyz", Want: []int{http.StatusOK},
	}}
	if !computeEdge {
		checks = append(checks, wire.HandoffCheck{
			Name: "api", Network: "tcp", Address: publicAddr, Path: "/v1/apps", Host: "localhost",
			Want: []int{http.StatusUnauthorized, http.StatusTooManyRequests},
		})
	}
	return checks
}
