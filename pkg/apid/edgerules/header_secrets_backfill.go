package edgerules

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// headerSecretBackfillPage is how many rules or versions one scan reads.
const headerSecretBackfillPage = 200

// HeaderSecretBackfillReport counts one backfill pass. A rule or version
// counts as changed when someone wrote it after the pass read it; the next
// pass sees it again if it still holds a value.
type HeaderSecretBackfillReport struct {
	FleetNotReady    bool
	RulesSealed      int
	RulesChanged     int
	RulesFailed      int
	VersionsSealed   int
	VersionsChanged  int
	VersionsFailed   int
	VersionsNoApp    int
	SecretRowsStored int
}

// Pending reports whether the pass left values to seal.
func (r HeaderSecretBackfillReport) Pending() bool {
	return r.FleetNotReady || r.RulesChanged+r.RulesFailed+r.VersionsChanged+r.VersionsFailed > 0
}

// SealStoredHeaderValues seals the credential header values that rules and
// rule-set versions still store as written (ADR-20261108110000, amendment
// 2026-11-09): values saved before sealing, and values saved while a serving
// gateway could not open sealed ones. It does nothing until every serving
// gateway can. A live rule changes through the usual convergence, so its
// rule-set gains a version; a stored version is rewritten in place, since no
// gateway reads it and a rollback restores whichever form it holds.
func (s *Service) SealStoredHeaderValues(ctx context.Context) (HeaderSecretBackfillReport, error) {
	var rep HeaderSecretBackfillReport
	literals, ok := s.Host.Store().(state.EdgeRuleHeaderLiteralStore)
	if !ok {
		return rep, nil
	}
	if _, ok := s.Host.Store().(state.EdgeRuleHeaderSecretStore); !ok || s.SealHeaderValue == nil {
		return rep, nil // sealed header values are not wired on this deployment
	}
	if !s.FleetOpensHeaderSecrets(ctx) {
		rep.FleetNotReady = true
		return rep, nil
	}
	sealed := map[string]string{}
	if err := s.backfillRules(ctx, literals, sealed, &rep); err != nil {
		return rep, err
	}
	if err := s.backfillVersions(ctx, literals, sealed, &rep); err != nil {
		return rep, err
	}
	rep.SecretRowsStored = len(sealed)
	return rep, nil
}

// legacyHeaderValues reports whether action stores a value that is kept
// sealed as written.
func legacyHeaderValues(action state.EdgeRuleAction) bool {
	redacted := RedactHeaderSecrets(action)
	if redacted.Headers == nil {
		return false
	}
	for _, op := range redacted.Headers.RequestHeaders {
		if op.Redacted {
			return true
		}
	}
	return false
}

func (s *Service) backfillRules(ctx context.Context, literals state.EdgeRuleHeaderLiteralStore, sealed map[string]string, rep *HeaderSecretBackfillReport) error {
	after := ""
	for {
		page, err := literals.ListEdgeRulesWithRequestHeaderLiterals(ctx, after, headerSecretBackfillPage)
		if err != nil {
			return err
		}
		for _, ref := range page {
			after = ref.ID
			if !legacyHeaderValues(ref.Action) {
				continue
			}
			switch s.sealStoredRule(ctx, ref.ID, sealed) {
			case backfillSealed:
				rep.RulesSealed++
			case backfillChanged:
				rep.RulesChanged++
			case backfillFleetNotReady:
				rep.FleetNotReady = true
				return nil
			default:
				rep.RulesFailed++
			}
		}
		if len(page) < headerSecretBackfillPage {
			return nil
		}
	}
}

type backfillOutcome int

const (
	backfillFailed backfillOutcome = iota
	backfillSealed
	backfillChanged
	backfillFleetNotReady
)

func (s *Service) sealStoredRule(ctx context.Context, ruleID string, sealed map[string]string) backfillOutcome {
	log := s.Host.Logger().With("rule", ruleID)
	row, err := s.Host.Store().GetEdgeRuleByID(ctx, ruleID)
	if errors.Is(err, state.ErrNotFound) {
		return backfillChanged
	}
	if err != nil {
		log.Warn("edge rule header backfill: read rule failed", "err", err)
		return backfillFailed
	}
	app, err := s.Host.Store().AppByID(ctx, row.AppID)
	if err != nil {
		log.Warn("edge rule header backfill: read app failed", "app", row.AppID, "err", err)
		return backfillFailed
	}
	action := RedactHeaderSecrets(row.Action)
	if prob := s.sealHeaderSecretsWith(ctx, app, &action, &row, sealed); prob != nil {
		log.Warn("edge rule header backfill: seal failed", "detail", prob.Detail)
		return backfillFailed
	}
	if legacyHeaderValues(action) {
		return backfillFleetNotReady // a gateway stopped opening sealed values mid-pass
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, row.AppID, row.ID, "updated", row.MatchHost)
	if err != nil {
		log.Warn("edge rule header backfill: convergence unavailable", "err", err)
		return backfillFailed
	}
	current, err := s.Host.Store().GetEdgeRuleByID(ctx, row.ID)
	if err != nil || !current.UpdatedAt.Equal(row.UpdatedAt) {
		convergence.Abort(ctx)
		return backfillChanged
	}
	updated, err := s.Host.Store().UpdateEdgeRule(ctx, row.ID, state.UpdateEdgeRuleParams{Action: &action})
	if err != nil {
		convergence.Abort(ctx)
		log.Warn("edge rule header backfill: update failed", "err", err)
		return backfillFailed
	}
	s.Host.EmitAudit(ctx, "edge_rule.header_values_sealed", &row.AccountID, map[string]any{
		"rule_id": row.ID, "app_id": row.AppID,
	})
	if err := convergence.Apply(ctx, updated.ID); err != nil {
		// The gateway serves the same headers either way; it reloads on the
		// next change notification.
		log.Warn("edge rule header backfill: sealed rule saved, fleet acknowledgement incomplete", "err", err)
	}
	return backfillSealed
}

func (s *Service) backfillVersions(ctx context.Context, literals state.EdgeRuleHeaderLiteralStore, sealed map[string]string, rep *HeaderSecretBackfillReport) error {
	var after int64
	apps := map[string]*state.App{}
	for {
		page, err := literals.ListEdgeRuleSetVersionsWithRequestHeaderLiterals(ctx, after, headerSecretBackfillPage)
		if err != nil {
			return err
		}
		for _, v := range page {
			after = v.Cursor
			app, ok := apps[v.AppID]
			if !ok {
				if a, err := s.Host.Store().AppByID(ctx, v.AppID); err == nil {
					app = &a
				}
				apps[v.AppID] = app
			}
			if app == nil {
				rep.VersionsNoApp++
				continue
			}
			actions := map[string]state.EdgeRuleAction{}
			failed := false
			for i := range v.Rules {
				rule := v.Rules[i]
				if !legacyHeaderValues(rule.Action) {
					continue
				}
				action := RedactHeaderSecrets(rule.Action)
				if prob := s.sealHeaderSecretsWith(ctx, *app, &action, &rule, sealed); prob != nil {
					s.Host.Logger().Warn("edge rule header backfill: seal version failed", "app", v.AppID, "version", v.Version, "detail", prob.Detail)
					failed = true
					break
				}
				if legacyHeaderValues(action) {
					rep.FleetNotReady = true
					return nil
				}
				actions[rule.ID] = action
			}
			switch {
			case failed:
				rep.VersionsFailed++
			case len(actions) == 0:
			default:
				replaced, err := literals.ReplaceEdgeRuleSetVersionActions(ctx, v, actions)
				switch {
				case err != nil:
					s.Host.Logger().Warn("edge rule header backfill: rewrite version failed", "app", v.AppID, "version", v.Version, "err", err)
					rep.VersionsFailed++
				case !replaced:
					rep.VersionsChanged++
				default:
					rep.VersionsSealed++
				}
			}
		}
		if len(page) < headerSecretBackfillPage {
			return nil
		}
	}
}

// RunHeaderSecretBackfill runs SealStoredHeaderValues every interval until
// ctx ends. A pass that finds nothing to seal costs one scan of each table.
func (s *Service) RunHeaderSecretBackfill(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = api.EdgeRuleHeaderSecretBackfillInterval
	}
	timer := time.NewTimer(api.EdgeRuleHeaderSecretBackfillStartDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		rep, err := s.SealStoredHeaderValues(ctx)
		switch {
		case err != nil:
			s.Host.Logger().Warn("edge rule header backfill pass failed", "err", err)
		case rep.RulesSealed+rep.VersionsSealed > 0 || rep.Pending():
			s.Host.Logger().Info("edge rule header backfill pass",
				"fleet_not_ready", rep.FleetNotReady, "rules_sealed", rep.RulesSealed, "rules_changed", rep.RulesChanged,
				"rules_failed", rep.RulesFailed, "versions_sealed", rep.VersionsSealed, "versions_changed", rep.VersionsChanged,
				"versions_failed", rep.VersionsFailed, "versions_without_app", rep.VersionsNoApp, "secret_rows", rep.SecretRowsStored)
		}
		timer.Reset(interval)
	}
}
