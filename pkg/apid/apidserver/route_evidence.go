package apidserver

import "github.com/onebox-faas/faas/pkg/apid/routeevidence"

// routeEvidence reads gateway-measured route feature evidence (ADR-1152)
// through the server's Prometheus client, read at call time.
func (s *server) routeEvidence() routeevidence.Reader {
	r := routeevidence.Reader{Apps: s.store}
	if s.promqlClient != nil {
		r.Prom = s.promqlClient
	}
	return r
}
