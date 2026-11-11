// adr: 1451 — customer config writes record revisions and open config guards.
package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// configChange tracks one customer config write: the baseline recorded
// before it and the guard it opened or joined.
type configChange struct {
	store  state.ConfigRevisionStore
	ticket state.ConfigChangeTicket
	acct   state.Account
	app    state.App
	scope  string
	actor  string
}

// configActor names who changed the config: the API key, or the account.
func configActor(r *http.Request, acct state.Account) string {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.ID != "" {
		return "api_key:" + p.Key.ID
	}
	if acct.Email != "" {
		return acct.Email
	}
	return "account:" + acct.ID
}

// configGuardRequested is false for `?guard=false` (CLI --no-guard).
func configGuardRequested(r *http.Request) bool {
	return r.URL.Query().Get("guard") != "false"
}

// beginConfigChange runs before a customer config write. A guard opens only
// when the request did not opt out and the scope has a live deployment
// whose traffic can judge the change. Bookkeeping failures never block the
// write; they leave the change unguarded and are audited.
// configGuardHasTraffic reports whether a live deployment's traffic can judge
// a config change in scope. A default-scope value overlays every
// environment, so a project app serving production in the `production`
// scope is judged there (ADR-1451 amendment 1).
func (s *server) configGuardHasTraffic(ctx context.Context, appID, scope string) bool {
	if _, err := s.store.LiveDeploymentForScope(ctx, appID, scope); err == nil {
		return true
	}
	if scope != "" && scope != state.DefaultEnvScope {
		return false
	}
	_, err := s.store.LiveDeploymentForScope(ctx, appID, "production")
	return err == nil
}

func (s *server) beginConfigChange(r *http.Request, acct state.Account, app state.App, scope, secretKey string) *configChange {
	store, ok := s.store.(state.ConfigRevisionStore)
	if !ok {
		return nil
	}
	guard := configGuardRequested(r)
	if guard {
		guard = s.configGuardHasTraffic(r.Context(), app.ID, scope)
	}
	return s.beginConfigChangeAs(r.Context(), store, acct, app, scope, configActor(r, acct), guard, secretKey)
}

// beginConfigChangeAs records the baseline for a config write made outside an
// HTTP request, such as a repository push, attributed to actor.
func (s *server) beginConfigChangeAs(ctx context.Context, store state.ConfigRevisionStore, acct state.Account, app state.App, scope, actor string, guard bool, secretKey string) *configChange {
	c := &configChange{store: store, acct: acct, app: app, scope: scope, actor: actor}
	start := state.ConfigChangeStart{AccountID: acct.ID, AppID: app.ID, Scope: scope, Actor: actor, Guard: guard, Now: time.Now().UTC()}
	if guard {
		start.SecretKey = secretKey
	}
	ticket, err := store.BeginConfigChange(ctx, start)
	if err != nil {
		s.configRevisionFailed(ctx, acct, app, scope, "begin", err)
		return nil
	}
	c.ticket = ticket
	return c
}

// finishConfigChange records the revision after a write landed.
func (s *server) finishConfigChange(ctx context.Context, c *configChange, source, key string, unrestorable bool) *state.ConfigRevision {
	if c == nil {
		return nil
	}
	revision, err := c.store.FinishConfigChange(ctx, state.ConfigChangeFinish{AccountID: c.acct.ID, AppID: c.app.ID,
		Scope: c.scope, Actor: c.actor, Source: source, Key: key, GuardID: c.ticket.GuardID,
		Unrestorable: unrestorable, Now: time.Now().UTC()})
	if err != nil {
		s.configRevisionFailed(ctx, c.acct, c.app, c.scope, "finish", err)
		return nil
	}
	return &revision
}

// abandonConfigChange closes a guard the failed write was the only change in.
func (s *server) abandonConfigChange(ctx context.Context, c *configChange) {
	if c == nil || c.ticket.GuardID == "" {
		return
	}
	if err := c.store.AbandonConfigChange(ctx, c.ticket.GuardID, time.Now().UTC()); err != nil {
		s.log.Warn("config guard: abandon after a failed write", "app", c.app.ID, "guard", c.ticket.GuardID, "err", err)
	}
}

func (s *server) configRevisionFailed(ctx context.Context, acct state.Account, app state.App, scope, stage string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Warn("config revision: bookkeeping failed; the change is unguarded", "app", app.ID, "scope", scope, "stage", stage, "err", err)
	s.audit.Emit(ctx, "config.revision_failed", &acct.ID, map[string]any{"app_id": app.ID, "scope": scope, "stage": stage})
}

// configChangeReceipt is the additive part of an env or secret write
// response: the revision the write recorded and the guard watching it.
func configChangeReceipt(c *configChange, revision *state.ConfigRevision) *api.ConfigChangeReceipt {
	if c == nil || revision == nil {
		return nil
	}
	receipt := &api.ConfigChangeReceipt{Revision: revision.Revision}
	if c.ticket.GuardID != "" {
		receipt.Guard = &api.ConfigGuardSummary{ID: c.ticket.GuardID, Status: state.ConfigGuardWatching,
			BaseRevision: c.ticket.BaseRevision, WindowMinutes: api.ConfigGuardWindowMinutes}
	}
	return receipt
}
