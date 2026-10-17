// adr: 1359
package vmmdgrpc

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// These headers are authored by the synthetic dispatcher; gateway strips them
// from public and private customer requests. Durable enrollment independently
// checks the invocation, live lease, instance, deployment and original recorder.
func (s *Server) beginDispatchActivity(r *vmmdpb.ForwardHTTPRequestInit) func() {
	if s.activity == nil {
		return func() {}
	}

	// Inspect only platform dispatch metadata; ordinary requests allocate no
	// header map on this shared production forwarding path.
	one := func(name string) string {
		value, count := "", 0
		for _, h := range r.GetHeaders() {
			if strings.EqualFold(h.GetName(), name) {
				value = h.GetValue()
				count++
			}
		}
		if count == 1 {
			return value
		}
		return ""
	}
	id, err := uuid.Parse(one(api.InvocationIDHeader))
	attempt, parseErr := strconv.Atoi(one(api.InvocationAttemptHeader))
	if err != nil || id == uuid.Nil || parseErr != nil || attempt != 1 || one(api.InvocationSourceHeader) != "async_invoke" {
		s.beginActivity(r.GetInstance())
		return func() { s.endActivity(r.GetInstance()) }
	}
	s.activity.BeginDispatch(r.GetInstance(), id.String(), attempt)
	return func() { s.activity.EndDispatch(r.GetInstance(), id.String(), attempt) }
}
