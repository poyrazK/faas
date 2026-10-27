// Package agents serves the Gregale Agents API (ADR-20261027090010): agent
// definitions, their immutable versions, and run admission. apid stores
// intents only; agentd (ADR-20261027090014) claims queued runs and owns every later
// transition. The routes belong to the internal agents capability
// (ADR-20261023115741) and answer like unregistered routes to every account
// it is not released to.
package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

const defaultPageSize = 50

// Store is the state the handlers need; *agentstore.Store implements it.
type Store interface {
	CreateAgent(ctx context.Context, accountID, name, description string, spec agentstore.VersionSpec, maxAgents int) (agentstore.Agent, agentstore.Version, error)
	AddVersion(ctx context.Context, accountID, name string, spec agentstore.VersionSpec, maxVersions int) (agentstore.Agent, agentstore.Version, error)
	Agent(ctx context.Context, accountID, name string) (agentstore.Agent, agentstore.Version, error)
	Version(ctx context.Context, accountID, name string, version int) (agentstore.Version, error)
	Agents(ctx context.Context, accountID, after string, limit int) ([]agentstore.Agent, error)
	DeleteAgent(ctx context.Context, accountID, name string) error
	AdmitRun(ctx context.Context, a agentstore.Admission) (agentstore.Run, bool, error)
	Run(ctx context.Context, accountID, id string) (agentstore.Run, error)
	Runs(ctx context.Context, accountID string, f agentstore.RunFilter) ([]agentstore.Run, error)
	CancelRun(ctx context.Context, accountID, id string) (agentstore.Run, error)
	RunOutputs(ctx context.Context, accountID, id string) (agentstore.Outputs, error)
}

// Handler serves the agent routes.
type Handler struct {
	Store Store
	// Files is the artifact store holding runs' output files; nil: files
	// cannot be downloaded from this host.
	Files storage.StorageBackend
	Audit func(ctx context.Context, action, accountID string, fields map[string]any)
	Log   *slog.Logger
}

// CreateAgent handles POST /v1/agents.
func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	maxAgents := api.AgentsPerAccount[acct.Plan.PlanIndex()]
	if maxAgents == 0 {
		api.WriteProblem(w, api.ErrAgentLimit(acct.Plan, "AgentsPerAccount", 0, 1))
		return
	}
	var body api.CreateAgentRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, api.AgentRequestMaxBytes) {
		return
	}
	if prob := firstProblem(validName(body.Name), validDescription(body.Description)); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	spec, prob := versionSpec(body.AgentVersionSpec, acct.Plan)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	agent, version, err := h.Store.CreateAgent(r.Context(), acct.ID, body.Name, body.Description, spec, maxAgents)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.create", err))
		return
	}
	h.Audit(r.Context(), "agent.created", acct.ID, map[string]any{"agent_id": agent.ID, "name": agent.Name, "version": version.Version})
	httpkit.WriteJSON(w, http.StatusCreated, agentJSON(agent, &version))
}

// ListAgents handles GET /v1/agents.
func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, prob := pageSize(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	list, err := h.Store.Agents(r.Context(), acct.ID, r.URL.Query().Get("after"), limit+1)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.list", err))
		return
	}
	out := api.AgentList{Items: make([]api.Agent, 0, min(len(list), limit))}
	for i, agent := range list {
		if i == limit {
			out.NextAfter = list[i-1].Name
			break
		}
		out.Items = append(out.Items, agentJSON(agent, nil))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetAgent handles GET /v1/agents/{agent}.
func (h *Handler) GetAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	agent, version, err := h.Store.Agent(r.Context(), acct.ID, r.PathValue("agent"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, agentJSON(agent, &version))
}

// DeleteAgent handles DELETE /v1/agents/{agent}.
func (h *Handler) DeleteAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name := r.PathValue("agent")
	if err := h.Store.DeleteAgent(r.Context(), acct.ID, name); err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.delete", err))
		return
	}
	h.Audit(r.Context(), "agent.deleted", acct.ID, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}

// CreateVersion handles POST /v1/agents/{agent}/versions.
func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var body api.AgentVersionSpec
	if !httpkit.DecodeJSONLimit(w, r, &body, api.AgentRequestMaxBytes) {
		return
	}
	spec, prob := versionSpec(body, acct.Plan)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	maxVersions := api.AgentVersionsPerAgent[acct.Plan.PlanIndex()]
	agent, version, err := h.Store.AddVersion(r.Context(), acct.ID, r.PathValue("agent"), spec, maxVersions)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.version_create", err))
		return
	}
	h.Audit(r.Context(), "agent.version_created", acct.ID, map[string]any{"agent_id": agent.ID, "name": agent.Name, "version": version.Version})
	httpkit.WriteJSON(w, http.StatusCreated, versionJSON(version))
}

// GetVersion handles GET /v1/agents/{agent}/versions/{version}.
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || number < 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeAgentNotFound, "Agent version not found", "versions are numbered from 1"))
		return
	}
	version, err := h.Store.Version(r.Context(), acct.ID, r.PathValue("agent"), number)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent.version_get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, versionJSON(version))
}

// CreateRun handles POST /v1/agents/{agent}/runs. A new run answers 202; a
// dedupe key already used answers 200 with the run that holds it.
func (h *Handler) CreateRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var body api.CreateAgentRunRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, api.AgentRequestMaxBytes) {
		return
	}
	admission, prob := runRequest(body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	admission.AccountID, admission.Agent = acct.ID, r.PathValue("agent")
	admission.Limits = agentstore.PlanLimits(acct.Plan)
	run, existing, err := h.Store.AdmitRun(r.Context(), admission)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.create", err))
		return
	}
	if existing {
		httpkit.WriteJSON(w, http.StatusOK, runJSON(run))
		return
	}
	h.Audit(r.Context(), "agent_run.created", acct.ID, map[string]any{"run_id": run.ID, "agent_id": run.AgentID,
		"version": run.AgentVersion, "tenant_id": run.TenantID})
	httpkit.WriteJSON(w, http.StatusAccepted, runJSON(run))
}

// ListRuns handles GET /v1/agent-runs, filtered by agent, state and tenant.
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	f, prob := h.runFilter(r, acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limit := f.Limit
	f.Limit++
	runs, err := h.Store.Runs(r.Context(), acct.ID, f)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.list", err))
		return
	}
	out := api.AgentRunList{Items: make([]api.AgentRun, 0, min(len(runs), limit))}
	for i, run := range runs {
		if i == limit {
			out.NextBefore = runs[i-1].ID
			break
		}
		out.Items = append(out.Items, runJSON(run))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetRun handles GET /v1/agent-runs/{id}.
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, err := h.Store.Run(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, runJSON(run))
}

// GetRunOutputs handles GET /v1/agent-runs/{id}/outputs: a finished run's
// result.json, output file manifest and log tail (ADR-20261027090010 §8). A run still
// in progress has none yet; one that ended without any returns empty
// outputs.
func (h *Handler) GetRunOutputs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, err := h.Store.Run(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.get", err))
		return
	}
	if !run.Terminal() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAgentRunNotFinished, "Run not finished",
			"the run has no outputs until it finishes"))
		return
	}
	out, err := h.Store.RunOutputs(r.Context(), acct.ID, run.ID)
	if err != nil && !errors.Is(err, agentstore.ErrNotFound) {
		api.WriteProblem(w, h.problem(acct, "agent_run.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, outputsJSON(out))
}

// GetRunOutputFile handles GET /v1/agent-runs/{id}/outputs/files/{name...}:
// the contents of one output file, as kept in the artifact store.
func (h *Handler) GetRunOutputFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	out, err := h.Store.RunOutputs(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.get", err))
		return
	}
	name := r.PathValue("name")
	for _, f := range out.Files {
		if f.Name != name || f.Key == "" {
			continue
		}
		if h.Files == nil {
			api.WriteProblem(w, api.ErrCapacity("output files are unavailable on this control-plane host"))
			return
		}
		body, err := h.Files.Get(r.Context(), f.Key)
		if err != nil {
			h.Log.Warn("agents: read output file", "run_id", r.PathValue("id"), "err", err)
			api.WriteProblem(w, api.ErrCapacity("the output file could not be read"))
			return
		}
		defer func() { _ = body.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(f.Size))
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(w, io.LimitReader(body, int64(f.Size)))
		return
	}
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeAgentOutputFileNotFound, "Output file not found",
		"the run kept no output file of that name"))
}

// CancelRun handles DELETE /v1/agent-runs/{id}: a queued run is cancelled at
// once; agentd stops a started one after a short grace period.
func (h *Handler) CancelRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, err := h.Store.CancelRun(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.cancel", err))
		return
	}
	h.Audit(r.Context(), "agent_run.cancel_requested", acct.ID, map[string]any{"run_id": run.ID, "state": run.State})
	httpkit.WriteJSON(w, http.StatusOK, runJSON(run))
}

func (h *Handler) runFilter(r *http.Request, acct state.Account) (agentstore.RunFilter, *api.Problem) {
	limit, prob := pageSize(r)
	if prob != nil {
		return agentstore.RunFilter{}, prob
	}
	q := r.URL.Query()
	f := agentstore.RunFilter{Agent: q.Get("agent"), State: q.Get("state"), Limit: limit}
	if f.State != "" && !runStates[f.State] {
		return f, api.ErrValidation("state is not a run state")
	}
	if tenant := q.Get("tenant"); tenant != "" {
		id, prob := runRequest(api.CreateAgentRunRequest{Tenant: tenant})
		if prob != nil {
			return f, prob
		}
		f.TenantID = id.TenantID
	}
	if before := q.Get("before"); before != "" {
		run, err := h.Store.Run(r.Context(), acct.ID, before)
		if err != nil {
			return f, h.problem(acct, "agent_run.list", err)
		}
		f.Before = &run
	}
	return f, nil
}

func pageSize(r *http.Request) (int, *api.Problem) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > api.AgentListPageMax {
		return 0, api.ErrValidation("limit must be between 1 and " + strconv.Itoa(api.AgentListPageMax))
	}
	return n, nil
}

// problem maps a store error to the customer's problem.
func (h *Handler) problem(acct state.Account, operation string, err error) *api.Problem {
	var limit *agentstore.LimitError
	var integration *agentstore.IntegrationError
	switch {
	case errors.As(err, &limit):
		return api.ErrAgentLimit(acct.Plan, limit.Limit, limit.Max, limit.Observed)
	case errors.As(err, &integration):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeAgentIntegrationInvalid, "Model integration not usable",
			fmt.Sprintf("integrations.%s must name an enabled customer %s AI integration of the account", integration.Provider, integration.Provider))
	case errors.Is(err, agentstore.ErrNotFound):
		return notFound(operation)
	case errors.Is(err, agentstore.ErrNameTaken):
		return api.NewProblem(http.StatusConflict, api.CodeAgentNameTaken, "Agent name taken", "the account already has an agent of that name")
	case errors.Is(err, agentstore.ErrBusy):
		return api.NewProblem(http.StatusConflict, api.CodeAgentBusy, "Agent has unfinished runs", "cancel or wait for the agent's runs before deleting it")
	case errors.Is(err, agentstore.ErrTenantNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeAgentTenantNotFound, "Tenant not found", "the account has no such platform tenant")
	case errors.Is(err, agentstore.ErrTenantInactive):
		return api.NewProblem(http.StatusConflict, api.CodeAgentTenantInactive, "Tenant not active", "the tenant is suspended")
	case errors.Is(err, agentstore.ErrCheckoutUnavailable):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeAgentCheckoutUnavailable, "Checkout unavailable",
			"the account has no such GitHub installation; install the Gregale GitHub App on the repository's owner first")
	case errors.Is(err, agentstore.ErrGitHubUnavailable):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeAgentGitHubUnavailable, "GitHub unavailable",
			"the account has no such GitHub installation; install the Gregale GitHub App on the repository's owner first")
	case errors.Is(err, agentstore.ErrDeliveryTargetMissing):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeAgentDeliveryTargetMissing, "Delivery target missing",
			"the agent version declares a github_comment delivery; name the issue or pull request in github")
	case errors.Is(err, agentstore.ErrBucketNotFound):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeAgentBucketNotFound, "Bucket not found",
			"a bucket delivery names a bucket the account does not have")
	case errors.Is(err, agentstore.ErrFinished):
		return api.NewProblem(http.StatusConflict, api.CodeAgentRunFinished, "Run already finished", "a finished run cannot be cancelled")
	}
	return httpkit.CustomerInternalProblem(h.Log, operation, "the agent request could not be completed", "retry the request", err)
}

// notFound names what was missing: a run, or an agent or its version.
func notFound(operation string) *api.Problem {
	switch operation {
	case "agent_run.get", "agent_run.cancel", "agent_run.list":
		return api.NewProblem(http.StatusNotFound, api.CodeAgentRunNotFound, "Agent run not found", "the account has no such agent run")
	}
	return api.NewProblem(http.StatusNotFound, api.CodeAgentNotFound, "Agent not found", "the account has no such agent or version")
}

func firstProblem(problems ...*api.Problem) *api.Problem {
	for _, p := range problems {
		if p != nil {
			return p
		}
	}
	return nil
}
