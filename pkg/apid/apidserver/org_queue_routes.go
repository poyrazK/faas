package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/queueaccess"
)

func (s *server) registerOrgQueueRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesProjectEnvironmentReadSurface...)(s.getProjectEnvironmentQueueBindings))))
	mux.HandleFunc("PUT /v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.replaceProjectEnvironmentQueueBindings))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queue-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listQueueBindings))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queue-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(productionQueueBindingHandler(s.idempotent(s.createQueueBinding))))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queue-bindings/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getQueueBinding))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queue-bindings/{id}/status", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getQueueBindingStatus))))
	mux.HandleFunc("PATCH /v1/orgs/{organization}/apps/{slug}/queue-bindings/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateQueueBinding))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/apps/{slug}/queue-bindings/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteQueueBinding))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/send", s.authLimited(s.requireMFA(s.requireScope(api.ScopesQueuesSendSurface...)(productionQueueBindingHandler(s.idempotent(s.queueSend))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/receive", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.queueReceive))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/{id}/ack", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(productionQueueBindingHandler(s.idempotent(s.queueAck))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases", s.queueLeaseRoute(s.queueLeases.Lease))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases:ack", s.queueLeaseRoute(s.queueLeases.Ack))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases:nack", s.queueLeaseRoute(s.queueLeases.Nack))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases:extend", s.queueLeaseRoute(s.queueLeases.Extend))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queues/state", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queueState))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queues/peek", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queuePeek))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/queues/dead_letter", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queueDeadLetter))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/queues/dead_letter/{id}/replay", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(productionQueueBindingHandler(s.idempotent(s.queueDeadLetterReplay))))))
	mux.HandleFunc("GET /v1/orgs/{organization}/queue-usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(accountHandler(queueaccess.Usage(s.store))))))
}
