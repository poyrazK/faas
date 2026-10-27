package apidserver

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// configureAgents wires the ADR-20261027090010 agent API. The agents
// capability (ADR-20261023115741) decides who sees it: the catalog gives it
// /v1/agents and /v1/agent-runs, and it is internal, so it is hidden from
// every account until an operator releases it. For a released account,
// without Postgres the routes answer capacity_unavailable.
func (s *server) configureAgents(pool *pgxpool.Pool) {
	h := &agents.Handler{
		Audit: func(ctx context.Context, action, accountID string, fields map[string]any) {
			s.audit.Emit(ctx, action, &accountID, fields)
		},
		Log: s.log,
	}
	if pool != nil {
		h.Store = agentstore.New(pool)
	}
	s.agentAPI = h
}

// agentsCapability is the product capability of the agent API, the
// automation agent step and agent trigger targets.
const agentsCapability = "agents"

// agentStepsAllowed reports whether acct may define workflow agent steps and
// agent trigger targets: only while the agents capability is on for it
// (ADR-20261027090010 §1). Without a capability gate (unit tests) every
// capability is on.
func (s *server) agentStepsAllowed(acct state.Account) bool {
	return s.capGate == nil || s.capGate.Enabled(agentsCapability, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
}

// agentPathHidden keeps a method mismatch on an agent route a 404 while the
// agents capability is not on for every account, so a 405 never reveals the
// routes to an account it is hidden from.
func (s *server) agentPathHidden(r *http.Request) bool {
	return s.capGate != nil && agents.IsAgentPath(r.URL.Path) && !s.capGate.ReleasedByDefault(agentsCapability)
}

// agentHandlers returns the configured handler, or answers
// capacity_unavailable when this apid has no agent store.
func (s *server) agentHandlers(w http.ResponseWriter) (*agents.Handler, bool) {
	if s.agentAPI == nil || s.agentAPI.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("the agent API is unavailable on this control-plane host"))
		return nil, false
	}
	return s.agentAPI, true
}

// withAgentOutputFiles lets the agent API serve output files from the
// artifact store agentd keeps them in. Startup wiring only.
func (s *server) withAgentOutputFiles(backend storage.StorageBackend) {
	if s.agentAPI != nil {
		s.agentAPI.Files = backend
	}
}

func (s *server) getAgentRunOutputFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.GetRunOutputFile(w, r, acct)
	}
}

func (s *server) createAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.CreateAgent(w, r, acct)
	}
}

func (s *server) listAgents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.ListAgents(w, r, acct)
	}
}

func (s *server) getAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.GetAgent(w, r, acct)
	}
}

func (s *server) deleteAgent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.DeleteAgent(w, r, acct)
	}
}

func (s *server) createAgentVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.CreateVersion(w, r, acct)
	}
}

func (s *server) getAgentVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.GetVersion(w, r, acct)
	}
}

func (s *server) createAgentRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.CreateRun(w, r, acct)
	}
}

func (s *server) listAgentRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.ListRuns(w, r, acct)
	}
}

func (s *server) getAgentRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.GetRun(w, r, acct)
	}
}

func (s *server) getAgentRunOutputs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.GetRunOutputs(w, r, acct)
	}
}

func (s *server) cancelAgentRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.CancelRun(w, r, acct)
	}
}
