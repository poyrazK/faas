package sandboxes

// Exposed ports (ADR-20261027090013 §11): expose a live session's guest port
// at https://<port>-<sandbox id>.<preview domain>, list, read and remove its
// ports. A private port's view carries a preview token, good for that port
// alone; a public port (SandboxPublicPreview) needs none. gatewayd-internal
// serves the preview hosts.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// ExposePort handles POST /v1/sandboxes/{id}/ports.
func (h *Handler) ExposePort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.previewsConfigured(w) {
		return
	}
	var body api.ExposeSandboxPortRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	if body.Visibility == "" {
		body.Visibility = api.SandboxPortPrivate
	}
	if prob := portRequestProblem(acct, body); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	idx := acct.Plan.PlanIndex()
	port, err := h.Store.ExposeSandboxPort(r.Context(), acct.ID, r.PathValue("id"), body.Port, body.Visibility, api.SandboxExposedPortsMax[idx])
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.port.expose", err))
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "sandbox.port.exposed", acct.ID, map[string]any{"sandbox_id": r.PathValue("id"), "port": port.Port,
			"visibility": port.Visibility})
	}
	h.writePort(w, r, acct, http.StatusCreated, port)
}

func portRequestProblem(acct state.Account, body api.ExposeSandboxPortRequest) *api.Problem {
	if err := sandboxproto.ValidPreviewPort(body.Port); err != nil {
		return api.ErrValidation("port must be 1-65535 and not one the platform reserves in the guest")
	}
	switch body.Visibility {
	case api.SandboxPortPrivate:
	case api.SandboxPortPublic:
		if !api.SandboxPublicPreview[acct.Plan.PlanIndex()] {
			return api.ErrSandboxPublicPreviewGated(acct.Plan)
		}
	default:
		return api.ErrValidation("visibility must be private or public")
	}
	return nil
}

// ListPorts handles GET /v1/sandboxes/{id}/ports. The list carries no
// preview tokens; read one port for its token.
func (h *Handler) ListPorts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.previewsConfigured(w) {
		return
	}
	sb, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.port.list", err))
		return
	}
	ports, err := h.Store.SandboxPorts(r.Context(), acct.ID, sb.ID)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.port.list", err))
		return
	}
	out := api.SandboxPortList{Items: make([]api.SandboxPort, 0, len(ports))}
	for _, p := range ports {
		out.Items = append(out.Items, h.portView(sb.ID, p))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetPort handles GET /v1/sandboxes/{id}/ports/{port}: the port with a
// fresh preview token while the session is live.
func (h *Handler) GetPort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.previewsConfigured(w) {
		return
	}
	n, ok := pathPort(w, r)
	if !ok {
		return
	}
	ports, err := h.Store.SandboxPorts(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.port.get", err))
		return
	}
	for _, p := range ports {
		if p.Port == n {
			h.writePort(w, r, acct, http.StatusOK, p)
			return
		}
	}
	api.WriteProblem(w, portNotFound())
}

// UnexposePort handles DELETE /v1/sandboxes/{id}/ports/{port}. Gateways
// stop routing the port within their route cache's lifetime.
func (h *Handler) UnexposePort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.previewsConfigured(w) {
		return
	}
	n, ok := pathPort(w, r)
	if !ok {
		return
	}
	if err := h.Store.UnexposeSandboxPort(r.Context(), acct.ID, r.PathValue("id"), n); err != nil {
		if errors.Is(err, agentstore.ErrNotFound) {
			api.WriteProblem(w, portNotFound())
			return
		}
		api.WriteProblem(w, h.problem(acct, "sandbox.port.unexpose", err))
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "sandbox.port.unexposed", acct.ID, map[string]any{"sandbox_id": r.PathValue("id"), "port": n})
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathPort(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || n < 1 || n > 65535 {
		api.WriteProblem(w, portNotFound())
		return 0, false
	}
	return n, true
}

func portNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeSandboxNotFound, "Port not found", "the sandbox session does not expose this port")
}

func (h *Handler) previewsConfigured(w http.ResponseWriter) bool {
	if !h.configured(w) {
		return false
	}
	if h.PreviewDomain == "" {
		api.WriteProblem(w, api.ErrSandboxPreviewsOff())
		return false
	}
	return true
}

func (h *Handler) portView(sandboxID string, p agentstore.SandboxPort) api.SandboxPort {
	return api.SandboxPort{Port: p.Port, Visibility: p.Visibility, CreatedAt: p.CreatedAt,
		URL: "https://" + api.SandboxPreviewHost(p.Port, sandboxID, h.PreviewDomain)}
}

// writePort writes a port with a preview token for a private port of a live
// session; the token ends with the session at the latest.
func (h *Handler) writePort(w http.ResponseWriter, r *http.Request, acct state.Account, status int, p agentstore.SandboxPort) {
	sb, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.port.get", err))
		return
	}
	out := h.portView(sb.ID, p)
	if p.Visibility == api.SandboxPortPrivate && !agentstore.SandboxTerminal(sb.State) && sb.CloseRequestedAt == nil {
		now := h.now().UTC()
		expires := now.Add(api.SandboxPreviewTokenTTLSec * time.Second)
		if sb.ExpiresAt.Before(expires) {
			expires = sb.ExpiresAt
		}
		token, err := h.Tokens.SignPreview(sandboxtoken.PreviewClaims{SandboxID: sb.ID, AccountID: sb.AccountID, Port: p.Port,
			IssuedAt: now, ExpiresAt: expires})
		if err != nil {
			api.WriteProblem(w, h.problem(acct, "sandbox.port.token", err))
			return
		}
		out.PreviewToken, out.PreviewTokenExpiresAt = token, &expires
	}
	httpkit.WriteJSON(w, status, out)
}
