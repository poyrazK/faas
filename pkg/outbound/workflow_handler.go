package outbound

import (
	"bytes"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"strings"
)

// Workflow retries must consume persisted attempts. A fresh HTTP/1 connection
// prevents net/http from silently replaying Idempotency-Key requests after a
// reused connection closes, or retrying HTTP/2 streams outside that ledger.
func newWorkflowHTTPClient(client *http.Client) *http.Client {
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		// Arbitrary transports may retry internally; fail closed for workflows.
		return nil
	}
	clone := *client
	transport = transport.Clone()
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = nil
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	clone.Transport = newDependencyTransport(transport)
	return &clone
}

func (h *Handler) callerWorkflowIdentity(w http.ResponseWriter, r *http.Request, integration Integration, path string) (*WorkflowIdentity, bool) {
	raw := r.Header.Get(WorkflowIdentityHeader)
	if raw == "" {
		return nil, true
	}
	if r.URL.RawQuery != "" {
		writeProblem(w, http.StatusForbidden, "outbound_route_not_allowed", "Workflow outbound query parameters are unavailable", "")
		return nil, false
	}
	if r.Header.Get(ExecutionIdentityHeader) != "" || r.Header.Get(WorkloadIdentityHeader) != "" || r.Header.Get(TokenHeader) != "" || r.Header.Get(AppHeader) != "" {
		writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Outbound identity is ambiguous", "")
		return nil, false
	}
	if h.WorkflowAuthorizer == nil || h.workflowClient == nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_workflow_unavailable", "Workflow outbound authorization is unavailable", "")
		return nil, false
	}
	if integration.OwnerKind != IntegrationOwnerCustomer || integration.ProviderAuthMode != ProviderAuthManaged || integration.CredentialSource != CredentialSourceCustomerSealed {
		writeProblem(w, http.StatusForbidden, "outbound_workflow_not_authorized", "Workflow access is unavailable for this integration", "")
		return nil, false
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, api.WorkflowOutboundBodyMaxBytes+1))
		if err != nil || int64(len(body)) > api.WorkflowOutboundBodyMaxBytes {
			writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Workflow outbound body exceeds the limit", "")
			return nil, false
		}
		_ = r.Body.Close()
	}
	identity, err := h.WorkflowAuthorizer.AuthorizeWorkflow(r.Context(), raw, integration.ID, r.Method, path, body)
	if err != nil || identity.AccountID != integration.AccountID {
		status, code := http.StatusServiceUnavailable, "outbound_workflow_authorization_unavailable"
		if errors.Is(err, ErrWorkflowNotAuthorized) || err == nil {
			status, code = http.StatusForbidden, "outbound_workflow_not_authorized"
		}
		writeProblem(w, status, code, "Workflow outbound request is not authorized", "")
		return nil, false
	}
	r.Header.Set("Idempotency-Key", "workflow/"+identity.RunID+"/"+identity.StepName)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if len(body) == 0 {
		r.Body = http.NoBody
	}
	return &identity, true
}

// Workflow outputs never receive authentication/cookie headers. Reflected host
// assertions and managed credentials are rejected before any bytes are returned.
func writeWorkflowOutboundResponse(w http.ResponseWriter, r *http.Request, response *http.Response, authorization string) {
	body, err := io.ReadAll(io.LimitReader(response.Body, api.WorkflowOutboundBodyMaxBytes+1))
	if err != nil || int64(len(body)) > api.WorkflowOutboundBodyMaxBytes {
		writeProblem(w, http.StatusBadGateway, "outbound_response_too_large", "Workflow outbound response is incomplete or exceeds the limit", "")
		return
	}
	credential := authorization
	if _, suffix, ok := strings.Cut(authorization, " "); ok {
		credential = suffix
	}
	if credential != "" && bytes.Contains(body, []byte(credential)) || bytes.Contains(body, []byte(r.Header.Get(WorkflowIdentityHeader))) {
		writeProblem(w, http.StatusBadGateway, "outbound_response_sensitive", "Workflow outbound response contains sensitive authorization data", "")
		return
	}
	for _, name := range []string{"Content-Type", "Retry-After"} {
		if value := response.Header.Get(name); value != "" && !strings.Contains(value, authorization) && !strings.Contains(value, r.Header.Get(WorkflowIdentityHeader)) && (credential == "" || !strings.Contains(value, credential)) {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}
