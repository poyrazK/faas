package edgerules

// Attack mode (ADR-20261021220456): one app-wide challenge rule that the
// switch creates, extends and removes. It goes through the same fleet
// convergence and audit trail as any rule write, but skips the plan's rule
// quotas and the challenge plan gate, because it is the emergency switch on
// every plan. The rule's expiry ends it; gateways drop an expired rule on
// their own (ADR-961).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// GetAppEdgeAttackMode serves GET /v1/apps/{slug}/edge-protection/attack-mode.
func (s *Service) GetAppEdgeAttackMode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	row, err := s.attackModeRule(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read attack mode"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, AttackModeStatus(row, time.Now()))
}

// EnableAppEdgeAttackMode serves PUT /v1/apps/{slug}/edge-protection/attack-mode.
// An empty body applies the default duration.
func (s *Service) EnableAppEdgeAttackMode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.EnableEdgeAttackModeRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	m, prob := s.EnableAttackMode(r.Context(), acct, app, time.Duration(req.DurationSeconds)*time.Second)
	s.writeAttackMode(r.Context(), w, app.ID, m, prob)
}

// DisableAppEdgeAttackMode serves DELETE /v1/apps/{slug}/edge-protection/attack-mode.
// Switching off an app that is not in attack mode succeeds.
func (s *Service) DisableAppEdgeAttackMode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	m, prob := s.DisableAttackMode(r.Context(), acct, app)
	s.writeAttackMode(r.Context(), w, app.ID, m, prob)
}

// writeAttackMode answers a switch with the resulting status, or the
// problem with the convergence state when the fleet has not acknowledged.
func (s *Service) writeAttackMode(ctx context.Context, w http.ResponseWriter, appID string, m ruleMutation, prob *api.Problem) {
	if prob != nil {
		if m.converging {
			m.convergence.SetResponseState(w, "converging")
		}
		api.WriteProblem(w, prob)
		return
	}
	if m.convergence != nil {
		m.convergence.SetResponseState(w, "active")
	}
	s.setEdgeRuleSetETag(ctx, w, appID)
	var row *state.EdgeRule
	if m.row.ID != "" {
		row = &m.row
	}
	httpkit.WriteJSON(w, http.StatusOK, AttackModeStatus(row, time.Now()))
}

// AttackModeStatus describes an app's attack mode from its attack-mode rule
// (nil when it has none).
func AttackModeStatus(row *state.EdgeRule, now time.Time) api.EdgeAttackModeStatus {
	if row == nil {
		return api.EdgeAttackModeStatus{}
	}
	started := row.CreatedAt.UTC()
	out := api.EdgeAttackModeStatus{
		Active:    row.Enabled && !row.EdgeRuleExpired(now),
		RuleID:    row.ID,
		StartedAt: &started,
		ExpiresAt: utcTimePtr(row.ExpiresAt),
	}
	if c := row.Action.Challenge; c != nil {
		out.Difficulty, out.PassTTLSeconds = c.Difficulty, c.PassTTLSeconds
	}
	return out
}

// attackModeRule returns the app's attack-mode rule, or nil.
func (s *Service) attackModeRule(ctx context.Context, appID string) (*state.EdgeRule, error) {
	rules, err := s.Host.Store().ListEdgeRulesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].AttackMode() {
			return &rules[i], nil
		}
	}
	return nil, nil
}

// attackModeRuleParams is the rule attack mode serves: every host, path and
// method of the app, ahead of the app's own rules, enforced, with the
// default challenge. Verified search crawlers (ADR-973) are not challenged;
// verified credentials never are (ADR-831 amendment 7).
func attackModeRuleParams(acct state.Account, app state.App, until time.Time) state.CreateEdgeRuleParams {
	challenge := api.EdgeRuleChallengeAction{}
	_ = challenge.Validate() // fills the defaults
	return state.CreateEdgeRuleParams{
		AccountID:   acct.ID,
		AppID:       app.ID,
		ManifestKey: state.EdgeRuleAttackModeKey,
		MatchHost:   api.EdgeRuleAppHosts,
		MatchPath:   "/**",
		Priority:    0,
		Enabled:     true,
		Kind:        state.EdgeRuleKindChallenge,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindChallenge,
			Challenge: &state.EdgeRuleChallengeAction{
				Difficulty: challenge.Difficulty, PassTTLSeconds: challenge.PassTTLSeconds,
			},
		},
		Name:        api.EdgeAttackModeRuleName,
		Description: "Challenges every visitor until it ends. Managed by attack mode: change it with gregale edge-rules attack-mode.",
		ExpiresAt:   &until,
		Match:       &api.EdgeRuleMatchExpr{Field: "verified_bot", Op: "missing"},
		Mode:        state.EdgeRuleModeEnforce,
	}
}

// EnableAttackMode switches attack mode on for d from now. While it is on,
// it moves the end to now+d and keeps the start; once it has run out, it
// starts a fresh one. The API and the dashboard share it.
func (s *Service) EnableAttackMode(ctx context.Context, acct state.Account, app state.App, d time.Duration) (ruleMutation, *api.Problem) {
	var m ruleMutation
	if !s.AttackModeReleased(acct) {
		return m, api.NewProblem(http.StatusUnprocessableEntity, api.CodeEdgeFeatureUnavailable, "Edge feature not released",
			"Attack mode is not yet available to this account. Switching it off always works.")
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, app.ID, "", "updated", api.EdgeRuleAppHosts)
	if err != nil {
		return m, api.ErrCapacity("edge-rule fleet convergence is unavailable; attack mode was not switched on")
	}
	// Read under the per-app mutation lock, so two switches cannot both
	// create the rule.
	current, err := s.attackModeRule(ctx, app.ID)
	if err != nil {
		convergence.Abort(ctx)
		return m, api.ErrCapacity("could not read attack mode")
	}
	now := time.Now().UTC()
	until := now.Add(d).Truncate(time.Second)
	store := s.Host.Store()
	extended := current != nil && current.Enabled && !current.EdgeRuleExpired(now)
	var row state.EdgeRule
	if extended {
		expiry := &until
		row, err = store.UpdateEdgeRule(ctx, current.ID, state.UpdateEdgeRuleParams{ExpiresAt: &expiry})
	} else {
		if current != nil {
			if err = store.DeleteEdgeRule(ctx, current.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				convergence.Abort(ctx)
				return m, api.ErrCapacity("could not switch attack mode on")
			}
		}
		row, err = store.CreateEdgeRule(ctx, attackModeRuleParams(acct, app, until))
	}
	if err != nil {
		convergence.Abort(ctx)
		return m, api.ErrCapacity("could not switch attack mode on")
	}
	s.Host.Logger().Info("edge attack mode on", "app", app.Slug, "account", acct.ID, "rule", row.ID,
		"expires_at", until, "extended", extended)
	s.Host.EmitAudit(ctx, "edge_attack_mode.enabled", &acct.ID, map[string]any{
		httpkit.AuditKeyRuleID: row.ID,
		httpkit.AuditKeyAppID:  app.ID,
		"expires_at":           until,
		"extended":             extended,
	})
	m.row, m.convergence = row, convergence
	if err := convergence.Apply(ctx, row.ID); err != nil {
		m.converging = true
		s.Host.Logger().Error("attack mode saved but fleet convergence is incomplete", "rule", row.ID, "generation", convergence.Generation, "err", err)
		return m, api.ErrCapacity("attack mode was saved but the serving fleet has not acknowledged it; read attack mode before retrying")
	}
	return m, nil
}

// AttackModeReleased reports whether acct may switch attack mode on: it is a
// managed challenge rule, so both the edge_attack_mode and edge_challenge
// platform flags must include the account (ADR-20261021220456 amendment 1).
func (s *Service) AttackModeReleased(acct state.Account) bool {
	return s.Host.PlatformFlagEnabled(platformflags.EdgeAttackMode, acct) && s.Host.PlatformFlagEnabled(platformflags.EdgeChallenge, acct)
}

// DisableAttackMode switches attack mode off by removing the rule. It
// succeeds, and changes nothing, when the app has no attack-mode rule.
func (s *Service) DisableAttackMode(ctx context.Context, acct state.Account, app state.App) (ruleMutation, *api.Problem) {
	var m ruleMutation
	if current, err := s.attackModeRule(ctx, app.ID); err != nil || current == nil {
		if err != nil {
			return m, api.ErrCapacity("could not read attack mode")
		}
		return m, nil
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, app.ID, "", "deleted", api.EdgeRuleAppHosts)
	if err != nil {
		return m, api.ErrCapacity("edge-rule fleet convergence is unavailable; attack mode was not switched off")
	}
	// Re-read under the per-app mutation lock: a concurrent switch may
	// have replaced or removed the rule.
	current, err := s.attackModeRule(ctx, app.ID)
	if err != nil || current == nil {
		convergence.Abort(ctx)
		if err != nil {
			return m, api.ErrCapacity("could not read attack mode")
		}
		return ruleMutation{}, nil
	}
	if err := s.Host.Store().DeleteEdgeRule(ctx, current.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
		convergence.Abort(ctx)
		return m, api.ErrCapacity("could not switch attack mode off")
	}
	wasActive := current.Enabled && !current.EdgeRuleExpired(time.Now())
	s.Host.Logger().Info("edge attack mode off", "app", app.Slug, "account", acct.ID, "rule", current.ID, "was_active", wasActive)
	s.Host.EmitAudit(ctx, "edge_attack_mode.disabled", &acct.ID, map[string]any{
		httpkit.AuditKeyRuleID: current.ID,
		httpkit.AuditKeyAppID:  app.ID,
		"was_active":           wasActive,
	})
	m.convergence = convergence
	if err := convergence.Apply(ctx, current.ID); err != nil {
		m.converging = true
		s.Host.Logger().Error("attack mode removed but fleet convergence is incomplete", "rule", current.ID, "generation", convergence.Generation, "err", err)
		return m, api.ErrCapacity("attack mode was switched off but the serving fleet has not acknowledged it; read attack mode before retrying")
	}
	return m, nil
}
