package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/queueleases"
	"github.com/onebox-faas/faas/pkg/middleware"
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
	s.configureAgentCallbacks(h)
	s.agentAPI = h
}

// configureAgentCallbacks wires ADR-20261030125501 §3's callback URLs and
// §4's toolset, whose sleeps are realtime schedules on the inbox. The
// signing key derives from the session key, which every apid replica shares;
// a callback's account must have the agents capability when it posts.
func (s *server) configureAgentCallbacks(h *agents.Handler) {
	if inbox, ok := s.store.(agents.CallbackInbox); ok {
		h.Inbox = inbox
	}
	if revocations, ok := h.Store.(agents.CallbackRevocations); ok {
		h.Revocations = revocations
	}
	if sleeps, ok := s.store.(agents.SleepSchedules); ok {
		h.Sleeps = sleeps
	}
	if s.sessions != nil {
		if signer, err := agents.NewCallbackSigner(s.sessions.BindingKey()); err == nil {
			h.Callbacks = signer
		} else {
			s.log.Warn("agent callbacks disabled", "err", err)
		}
	}
	h.CallbackBaseURL = "https://api." + strings.TrimPrefix(strings.TrimSpace(s.platformHost()), ".")
	h.AgentsEnabled = func(ctx context.Context, accountID string) bool {
		acct, err := s.store.AccountByID(ctx, accountID)
		return err == nil && s.agentStepsAllowed(acct)
	}
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

// agentCallbackCallerRoute authenticates an issue or revoke request
// (ADR-20261030125501 step 4). A JWT-shaped bearer is a gregale:agents
// workload token from a live instance of an app, which reaches only that
// app's endpoints; every other credential takes the API-key chain, and the
// organization check an app route gets decides each endpoint it names. No
// API key, OIDC or hosting token is JWT-shaped, so the split never
// misroutes a key.
func (s *server) agentCallbackCallerRoute(next func(http.ResponseWriter, *http.Request, agents.CallbackCaller)) http.HandlerFunc {
	keyed := s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		next(w, r, agents.CallbackCaller{Account: acct, Authorize: func(endpoint state.ManagedRealtimeEndpoint) *api.Problem {
			return s.authorizeCallbackEndpoint(r, acct, endpoint)
		}})
	})))
	workload := middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			caller, err := agents.WorkloadCaller(r, s.workloadTrust(), s.store, time.Now())
			if err != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required",
					"a live workload token for gregale:agents from a production instance of the app is required"))
				return
			}
			// The API-key chain decides the capability for the account; a
			// workload token gets the same decision (ADR-20261023115741).
			if !s.capabilityAllowedFor(r, caller.Account) {
				writeMissingRoute(w, r)
				return
			}
			next(w, r, caller)
		}))
	return func(w http.ResponseWriter, r *http.Request) {
		if queueleases.WorkloadBearer(r) {
			workload.ServeHTTP(w, r)
			return
		}
		keyed(w, r)
	}
}

// authorizeCallbackEndpoint applies appaccess to the app of an endpoint an
// API-key caller names in the body. The route names no app, so without it
// a creator removed from, demoted in or suspended with a company app's
// organization, or a key bound to another organization, would still issue
// callback URLs into the app's agent inbox and revoke its threads.
func (s *server) authorizeCallbackEndpoint(r *http.Request, acct state.Account, endpoint state.ManagedRealtimeEndpoint) *api.Problem {
	app, err := s.store.AppByID(r.Context(), endpoint.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	if err != nil {
		return api.ErrCapacity("could not load the endpoint's app; retry shortly")
	}
	return s.authorizeNamedApp(r, acct, app)
}

func (s *server) createAgentCallback(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("the agent API is unavailable on this control-plane host"))
		return
	}
	s.agentAPI.CreateCallback(w, r, caller)
}

func (s *server) revokeAgentCallbacks(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("the agent API is unavailable on this control-plane host"))
		return
	}
	s.agentAPI.RevokeCallbacks(w, r, caller)
}

// receiveAgentCallback serves the token-only callback route; it needs the
// realtime store, not the agent store.
func (s *server) receiveAgentCallback(w http.ResponseWriter, r *http.Request) {
	if s.agentAPI == nil {
		http.NotFound(w, r)
		return
	}
	s.agentAPI.ReceiveCallback(w, r)
}

// The ADR-20261030125501 §4 toolset routes carry no account credential:
// an invocation is backed by the callback URL in its body.
func (s *server) getAgentToolset(w http.ResponseWriter, r *http.Request) {
	if s.agentAPI == nil {
		http.NotFound(w, r)
		return
	}
	s.agentAPI.Toolset(w, r)
}

func (s *server) invokeAgentTool(w http.ResponseWriter, r *http.Request) {
	if s.agentAPI == nil {
		http.NotFound(w, r)
		return
	}
	s.agentAPI.InvokeTool(w, r)
}

func (s *server) closeAgentToolThread(w http.ResponseWriter, r *http.Request) {
	if s.agentAPI == nil {
		http.NotFound(w, r)
		return
	}
	s.agentAPI.CloseThread(w, r)
}

func (s *server) cancelAgentRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h, ok := s.agentHandlers(w); ok {
		h.CancelRun(w, r, acct)
	}
}
