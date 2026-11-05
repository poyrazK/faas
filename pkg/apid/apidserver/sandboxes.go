package apidserver

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// configureSandboxes wires ADR-20261027090013's sandbox session intents.
// Their routes belong to the agents capability, like the agent API. The
// token key derives from the session key, which every apid replica shares;
// the data plane (rollout step 4) verifies the same tokens.
func (s *server) configureSandboxes(pool *pgxpool.Pool, getenv func(string) string) error {
	// ADR-20261027090013 §11: preview URLs are off until the operator
	// names a preview domain.
	domain, err := api.NormalizeSandboxPreviewDomain(getenv("FAAS_SANDBOX_PREVIEW_DOMAIN"))
	if err != nil {
		return err
	}
	h := &sandboxes.Handler{
		PreviewDomain: domain,
		Audit: func(ctx context.Context, action, accountID string, fields map[string]any) {
			s.audit.Emit(ctx, action, &accountID, fields)
		},
		Log: s.log,
	}
	if pool != nil {
		h.Store = agentstore.New(pool)
	}
	h.EnsureImage = s.ensureSessionImage
	if s.sessions != nil {
		// Ed25519, derived from apid's session key: gateways verify with
		// the public key from Auth.SandboxTokenKeys and cannot mint.
		if issuer, err := sandboxtoken.NewIssuer(s.sessions.BindingKey()); err == nil {
			h.Tokens = issuer
		} else {
			s.log.Warn("sandbox tokens disabled", "err", err)
		}
	}
	s.sandboxAPI = h
	return nil
}

// sandboxRoute serves a sandbox route, or capacity_unavailable on an apid
// configured without them.
func (s *server) sandboxRoute(next func(*sandboxes.Handler, http.ResponseWriter, *http.Request, state.Account)) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if s.sandboxAPI == nil {
			api.WriteProblem(w, api.ErrCapacity("sandbox sessions are unavailable on this control-plane host"))
			return
		}
		next(s.sandboxAPI, w, r, acct)
	}
}
