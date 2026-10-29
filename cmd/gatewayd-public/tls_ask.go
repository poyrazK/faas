package main

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// installOnDemandTLSAsk mounts ADR-520's certificate permission check on the
// control listener when FAAS_CUSTOM_DOMAIN_TLS=on_demand. The public edge
// (Caddy, same host) calls it before loading, obtaining or renewing a
// certificate for a customer hostname. Without the flag the path is absent
// and the edge's ask fails closed, so no customer certificate is issued.
func installOnDemandTLSAsk(mux *http.ServeMux, store gateway.OnDemandTLSStore, domains hostidentity.Domains, reg prometheus.Registerer, log *slog.Logger) bool {
	if !api.CustomDomainTLSOnDemand() {
		return false
	}
	policy := gateway.NewOnDemandTLSPolicy(store, domains.Apps, reg, log).RefuseOwned(domains.OwnedDomains())
	mux.Handle(gateway.OnDemandTLSAskPath, policy.Handler())
	log.Info("gatewayd-public: on-demand custom-domain TLS ask endpoint enabled", "path", gateway.OnDemandTLSAskPath)
	return true
}
