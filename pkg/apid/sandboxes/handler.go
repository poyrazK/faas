// Package sandboxes serves the control-plane routes of sandbox sessions
// (ADR-20261027090013 §3): create, list, read, change and close. apid stores
// intents only; agentd claims a queued session and owns every later
// transition (ADR-20261027090014). Creating or reading a live session returns
// a fresh session token for the data plane. The routes belong to the internal
// agents capability and answer like unregistered routes to every account it
// is not released to.
package sandboxes

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/resourcelabels"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Store is the state the handlers need; *agentstore.Store implements it.
type Store interface {
	CreateSandbox(ctx context.Context, a agentstore.SandboxAdmission) (agentstore.Sandbox, error)
	Sandbox(ctx context.Context, accountID, id string) (agentstore.Sandbox, error)
	Sandboxes(ctx context.Context, accountID string, f agentstore.SandboxFilter) ([]agentstore.Sandbox, error)
	UpdateSandboxTimeouts(ctx context.Context, accountID, id string, idleTimeoutSec int, expiresAt time.Time) (agentstore.Sandbox, error)
	CloseSandbox(ctx context.Context, accountID, id string) (agentstore.Sandbox, error)
}

// Handler serves the sandbox routes. Without a store or a token signer they
// answer capacity_unavailable. Now is time.Now unless set.
type Handler struct {
	Store  Store
	Tokens *sandboxtoken.Issuer
	Audit  func(ctx context.Context, action, accountID string, fields map[string]any)
	Log    *slog.Logger
	Now    func() time.Time
}

// requestMaxBytes bounds a create or change request.
const requestMaxBytes = 16 << 10

// Create handles POST /v1/sandboxes.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	var body createBody
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	admission, prob := h.admission(acct, body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	sb, err := h.Store.CreateSandbox(r.Context(), admission)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.create", err))
		return
	}
	h.audit(r.Context(), "sandbox.created", sb)
	httpkit.WriteJSON(w, http.StatusCreated, h.view(acct, sb, true))
}

// List handles GET /v1/sandboxes, filtered by state and by a label
// selector.
func (h *Handler) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	f, selector, prob := h.filter(r, acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limit := f.Limit
	f.Limit++
	rows, err := h.Store.Sandboxes(r.Context(), acct.ID, f)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.list", err))
		return
	}
	out := api.SandboxList{Items: []api.Sandbox{}}
	for i, sb := range rows {
		if i == limit {
			out.NextBefore = rows[i-1].ID
			break
		}
		if selector.Matches(sb.Labels) {
			out.Items = append(out.Items, h.view(acct, sb, false))
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Get handles GET /v1/sandboxes/{id}. A live session's view carries a
// fresh token, which is how a client resumes a session it serialized.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	sb, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, h.view(acct, sb, true))
}

// Update handles PATCH /v1/sandboxes/{id}: a live session's idle timeout
// or lifetime, within the plan's limits.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	var body api.UpdateSandboxRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	if body.IdleTimeoutS == nil && body.LifetimeS == nil {
		api.WriteProblem(w, api.ErrValidation("give idle_timeout_s, lifetime_s or both"))
		return
	}
	sb, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.update", err))
		return
	}
	idle, expires, prob := h.timeouts(acct, sb, body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if sb, err = h.Store.UpdateSandboxTimeouts(r.Context(), acct.ID, sb.ID, idle, expires); err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.update", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, h.view(acct, sb, false))
}

// Close handles DELETE /v1/sandboxes/{id}. A queued session closes at once;
// a live one closes when agentd tears its VM down. Closing an ended session
// changes nothing.
func (h *Handler) Close(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	sb, err := h.Store.CloseSandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.close", err))
		return
	}
	h.audit(r.Context(), "sandbox.closed", sb)
	httpkit.WriteJSON(w, http.StatusOK, h.view(acct, sb, false))
}

func (h *Handler) configured(w http.ResponseWriter) bool {
	if h.Store == nil || h.Tokens == nil {
		api.WriteProblem(w, api.ErrCapacity("sandbox sessions are unavailable on this control-plane host"))
		return false
	}
	return true
}

// filter reads a list request's state, page and selector.
func (h *Handler) filter(r *http.Request, acct state.Account) (agentstore.SandboxFilter, labels.Selector, *api.Problem) {
	q := r.URL.Query()
	f := agentstore.SandboxFilter{State: q.Get("state"), Limit: 50}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.AgentListPageMax {
			return f, labels.Selector{}, api.ErrValidation("limit must be between 1 and " + strconv.Itoa(api.AgentListPageMax))
		}
		f.Limit = n
	}
	if f.State != "" && !sandboxStates[f.State] {
		return f, labels.Selector{}, api.ErrValidation("state is not a sandbox state")
	}
	selector, prob := resourcelabels.ParseSelector(q)
	if prob != nil {
		return f, selector, prob
	}
	if before := q.Get("before"); before != "" {
		sb, err := h.Store.Sandbox(r.Context(), acct.ID, before)
		if err != nil {
			return f, selector, h.problem(acct, "sandbox.list", err)
		}
		f.Before = &sb
	}
	return f, selector, nil
}

var sandboxStates = map[string]bool{agentstore.SandboxQueued: true, agentstore.SandboxStarting: true, agentstore.SandboxReady: true,
	agentstore.SandboxFrozen: true, agentstore.SandboxClosed: true, agentstore.SandboxExpired: true, agentstore.SandboxFailed: true}

// view is a session as the API shows it. withToken mints a token for a
// live session; it ends with the session at the latest. A placed session's
// token names its node and instance, and carries the plan's command caps.
func (h *Handler) view(acct state.Account, sb agentstore.Sandbox, withToken bool) api.Sandbox {
	out := api.Sandbox{ID: sb.ID, State: sb.State, Kind: sb.Kind, Preset: sb.Preset, RAMMB: sb.RAMMB,
		Network: api.SandboxNetwork{Mode: sb.NetworkMode}, Tenant: sb.TenantID, IdleTimeoutS: sb.IdleTimeoutSec,
		ExpiresAt: sb.ExpiresAt, Labels: sb.Labels, CloseRequestedAt: sb.CloseRequestedAt, CreatedAt: sb.CreatedAt,
		UpdatedAt: sb.UpdatedAt, ClosedAt: sb.ClosedAt}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if sb.OutcomeCode != "" {
		out.Outcome = &api.SandboxOutcome{Code: sb.OutcomeCode, Message: sb.OutcomeMessage}
	}
	if !withToken || agentstore.SandboxTerminal(sb.State) || sb.CloseRequestedAt != nil {
		return out
	}
	now := h.now().UTC()
	expires := now.Add(api.SandboxTokenTTLSec * time.Second)
	if sb.ExpiresAt.Before(expires) {
		expires = sb.ExpiresAt
	}
	idx := acct.Plan.PlanIndex()
	token, err := h.Tokens.Sign(sandboxtoken.Claims{SandboxID: sb.ID, AccountID: sb.AccountID, TenantID: sb.TenantID,
		NodeID: sb.NodeID, InstanceID: sb.InstanceID, IssuedAt: now, ExpiresAt: expires,
		ExecTimeoutMaxMillis: int64(api.SandboxExecTimeoutMaxSec[idx]) * 1000, ExecOutputMaxBytes: int64(api.SandboxExecOutputMaxBytes[idx])})
	if err != nil {
		if h.Log != nil {
			h.Log.Error("sign sandbox token", "sandbox_id", sb.ID, "err", err)
		}
		return out
	}
	out.Token, out.TokenExpiresAt = token, &expires
	return out
}

// problem maps a store error to the customer's problem.
func (h *Handler) problem(acct state.Account, operation string, err error) *api.Problem {
	var limit *agentstore.LimitError
	switch {
	case errors.As(err, &limit):
		return api.ErrAgentLimit(acct.Plan, limit.Limit, limit.Max, limit.Observed)
	case errors.Is(err, agentstore.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeSandboxNotFound, "Sandbox not found", "the account has no such sandbox session")
	case errors.Is(err, agentstore.ErrFinished):
		return api.NewProblem(http.StatusConflict, api.CodeSandboxEnded, "Sandbox ended", "the session closed, expired or failed")
	case errors.Is(err, agentstore.ErrTenantNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeAgentTenantNotFound, "Tenant not found", "the account has no such platform tenant")
	case errors.Is(err, agentstore.ErrTenantInactive):
		return api.NewProblem(http.StatusConflict, api.CodeAgentTenantInactive, "Tenant not active", "the tenant is suspended")
	}
	return httpkit.CustomerInternalProblem(h.Log, operation, "the sandbox request could not be completed", "retry the request", err)
}

func (h *Handler) audit(ctx context.Context, action string, sb agentstore.Sandbox) {
	if h.Audit != nil {
		h.Audit(ctx, action, sb.AccountID, map[string]any{"sandbox_id": sb.ID, "tenant_id": sb.TenantID, "preset": sb.Preset})
	}
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
