package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (s *server) reserveEntityTenantConnection(w http.ResponseWriter, r *http.Request, id durableentity.ID) (string, func(), bool) {
	if s.durable.TenantQuotas == nil {
		return "", func() {}, true
	}
	quotas := s.durable.TenantQuotas
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	lease, err := quotas.AcquireConnection(ctx, id)
	if err != nil {
		durable.WriteTenantQuotaProblem(w, err)
		return "", nil, false
	}
	//nolint:contextcheck // Release retains the request values via WithoutCancel and has a bounded timeout after disconnect.
	return lease, func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		_ = quotas.ReleaseConnection(ctx, id, lease)
	}, true
}
