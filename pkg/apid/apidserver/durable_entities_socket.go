package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) connectDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.durable.Sockets == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_sockets_unavailable", "Entity sockets unavailable", "entity sockets are not enabled"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.durable.Engine == nil || !s.durable.Apps[app.ID] {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entities_unavailable", "Durable entities unavailable", "this app is not enabled for the durable entity preview"))
		return
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
		return
	}
	if !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("durable_entities_preview", acct.Plan))
		return
	}
	if !acct.Plan.WebSocketResponseAllowed() || !app.WebSocketEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("durable_entity_sockets", acct.Plan))
		return
	}
	selectors, err := durable.InspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity selectors"))
		return
	}
	request := api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}
	id, _, problem := s.durable.Identity(r, acct, app, request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	identity, err := json.Marshal(id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("encode entity scope"))
		return
	}
	// Re-enter the same credential/MFA/scope and entity admission chain for every
	// frame. Revocation, tenant suspension and deployment changes are rechecked.
	invoke := s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(func(w http.ResponseWriter, r *http.Request, current state.Account) {
		fresh, ok := s.loadApp(w, r, current, r.PathValue("slug"))
		if !ok {
			return
		}
		if s.durable.Sockets == nil || !current.Plan.WebSocketResponseAllowed() || !fresh.WebSocketEnabled {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Entity socket disabled", "socket admission changed"))
			return
		}
		s.durable.Invoke(w, r, current)
	})))
	if r.Header.Get("Origin") != "" {
		api.WriteProblem(w, api.ErrValidation("entity sockets accept service clients without an Origin header"))
		return
	}
	lease, release, ok := s.reserveEntityTenantConnection(w, r, id)
	if !ok {
		return
	}
	defer release()
	s.durable.Sockets.Serve(w, r, acct.ID, string(identity), func(frame entitysocket.Frame) entitysocket.Reply {
		if s.durable.TenantQuotas != nil {
			if err := s.durable.TenantQuotas.CheckConnection(r.Context(), id, lease); err != nil {
				if errors.Is(err, entitysocket.ErrGrantDenied) {
					return entitysocket.Reply{Status: 403}
				}
				return entitysocket.Reply{Status: 503}
			}
		}
		request.RequestID, request.Payload = frame.RequestID, frame.Payload
		body, err := json.Marshal(request)
		if err != nil {
			return entitysocket.Reply{Status: http.StatusBadRequest}
		}
		sub := r.Clone(context.WithValue(r.Context(), durable.SocketScopeFence{}, id))
		sub.Method = http.MethodPost
		sub.Body = io.NopCloser(bytes.NewReader(body))
		sub.ContentLength = int64(len(body))
		sub.Header = r.Header.Clone()
		sub.Header.Set("Content-Type", "application/json")
		sub.Header.Del("Upgrade")
		sub.Header.Del("Connection")
		sub.URL.Path = "/v1/apps/" + app.Slug + "/entities/invoke"
		sub.URL.RawQuery = ""
		sub.RequestURI = sub.URL.RequestURI()
		sub.Pattern = "POST /v1/apps/{slug}/entities/invoke"
		sub.SetPathValue("slug", app.Slug)
		reply := &entitySocketResponse{header: make(http.Header)}
		invoke(reply, sub)
		if reply.failed {
			return entitysocket.Reply{Status: http.StatusServiceUnavailable}
		}
		status := reply.status
		if status == 0 {
			status = http.StatusOK
		}
		result := entitysocket.Reply{Status: status}
		if status == http.StatusOK {
			var committed api.DurableEntityInvokeResponse
			if json.Unmarshal(reply.body.Bytes(), &committed) != nil {
				return entitysocket.Reply{Status: http.StatusServiceUnavailable}
			}
			result.Result = &committed
		} else if json.Valid(reply.body.Bytes()) {
			result.Problem = append(json.RawMessage(nil), reply.body.Bytes()...)
		}
		return result
	})
}

type entitySocketResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	failed bool
}

func (r *entitySocketResponse) Header() http.Header { return r.header }
func (r *entitySocketResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *entitySocketResponse) Write(body []byte) (int, error) {
	if r.body.Len()+len(body) > api.MaxDurableEntityInvocationBytes {
		r.failed = true
		return 0, errors.New("socket response too large")
	}
	return r.body.Write(body)
}
