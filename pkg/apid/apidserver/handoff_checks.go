package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/wire"
)

// HandoffChecks are the requests a serving apid sends its successor before
// the successor takes traffic (docs/adr/20261020214632-automatic-unit-deploy-
// safety-rails.md, section 4): readiness on the operator listener, when one
// is configured, and one API request without credentials on the public
// listener, which passes routing, middleware and authentication and is
// answered 401 without touching tenant data. A successor whose answers
// differ never takes a request; the deploy that started it fails and rolls
// back.
func HandoffChecks(listenAddr, metricsAddr string) []wire.HandoffCheck {
	var checks []wire.HandoffCheck
	if metricsAddr != "" {
		checks = append(checks, wire.HandoffCheck{
			Name: "readyz", Network: "tcp", Address: metricsAddr, Path: "/readyz", Want: []int{http.StatusOK},
		})
	}
	return append(checks, wire.HandoffCheck{
		Name: "api", Network: "tcp", Address: listenAddr, Path: "/v1/apps", Host: "localhost",
		Want: []int{http.StatusUnauthorized},
	})
}
