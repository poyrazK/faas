package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrMCPAccessPolicyChanged = errors.New("MCP access policy changed")

type mcpAccessPolicyContextKey struct{}
type mcpAccessPolicyGuard struct {
	AccountID, AppID string
	Expectation      api.MCPAccessPolicyExpectation
}

// WithMCPAccessPolicyExpectation adds an optional transaction-bound traffic guard.
// Account and app identities come from the authorized deployment, never the body.
func WithMCPAccessPolicyExpectation(ctx context.Context, accountID, appID string, expectation api.MCPAccessPolicyExpectation) context.Context {
	if expectation.AccountRevision != nil {
		revision := *expectation.AccountRevision
		expectation.AccountRevision = &revision
	}
	return context.WithValue(ctx, mcpAccessPolicyContextKey{}, mcpAccessPolicyGuard{accountID, appID, expectation})
}

func (m *MemStore) checkMCPAccessPolicyLocked(ctx context.Context, appID string) error {
	guard, ok := ctx.Value(mcpAccessPolicyContextKey{}).(mcpAccessPolicyGuard)
	if !ok {
		return nil
	}
	if appID != guard.AppID || guard.Expectation.Validate() != nil {
		return ErrMCPAccessPolicyChanged
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != guard.AccountID {
		return ErrMCPAccessPolicyChanged
	}
	rules := []api.EdgeRuleResponse{}
	for _, r := range m.edgeRules {
		if r.AccountID != guard.AccountID || r.Kind != EdgeRuleKindJWT {
			continue
		}
		action, err := json.Marshal(r.Action)
		if err != nil {
			return ErrMCPAccessPolicyChanged
		}
		rules = append(rules, api.EdgeRuleResponse{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, Kind: string(r.Kind), MatchHost: r.MatchHost, MatchPath: r.MatchPath, MatchMethods: r.MatchMethods, MatchHeaders: r.MatchHeaders, Priority: r.Priority, Enabled: r.Enabled, Action: action, ExpiresAt: r.ExpiresAt, Match: r.Match, Mode: r.Mode})
	}
	fingerprint, fingerprintErr := api.MCPAccessPolicyFingerprint(guard.Expectation.Resource, rules, time.Now())
	if fingerprintErr != nil {
		return ErrMCPAccessPolicyChanged
	}
	if err := observeMCPAdmission(ctx, fingerprint, nil); err != nil {
		return err
	}
	if guard.Expectation.Validate() != nil || fingerprint != guard.Expectation.Fingerprint {
		return ErrMCPAccessPolicyChanged
	}
	if guard.Expectation.AccountRevision != nil {
		return ErrMCPGatewayEvidenceUnavailable
	}
	return nil
}

func (s *PgStore) checkMCPAccessPolicy(ctx context.Context, tx pgx.Tx, appID string) error {
	guard, ok := ctx.Value(mcpAccessPolicyContextKey{}).(mcpAccessPolicyGuard)
	if !ok {
		return nil
	}
	if appID != guard.AppID || guard.Expectation.Validate() != nil {
		return ErrMCPAccessPolicyChanged
	}
	// Lock after deployment/app locks; no edge-rule rows are locked. All edge-rule
	// commits acquire this same account lock through a deferred constraint trigger.
	q := sqlc.New()
	actualAccount, err := q.ReadMCPAccessPolicyAppAccount(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return err
	}
	if pgUUIDString(actualAccount) != guard.AccountID {
		return ErrMCPAccessPolicyChanged
	}
	if err := q.LockMCPAccessPolicyAccount(ctx, tx, guard.AccountID); err != nil {
		return err
	}
	rows, err := q.ReadMCPAccessPolicyRules(ctx, tx, mustPgUUID(guard.AccountID))
	if err != nil {
		return err
	}
	rules := make([]api.EdgeRuleResponse, 0, len(rows))
	for _, r := range rows {
		var headers map[string]string
		var match *api.EdgeRuleMatchExpr
		if json.Unmarshal(r.MatchHeaders, &headers) != nil {
			return ErrMCPAccessPolicyChanged
		}
		if len(r.MatchExpr) > 0 && json.Unmarshal(r.MatchExpr, &match) != nil {
			return ErrMCPAccessPolicyChanged
		}
		var expires *time.Time
		if r.ExpiresAt.Valid {
			value := r.ExpiresAt.Time
			expires = &value
		}
		var action EdgeRuleAction
		if json.Unmarshal(r.Action, &action) != nil {
			return ErrMCPAccessPolicyChanged
		}
		actionBytes, err := json.Marshal(action)
		if err != nil {
			return ErrMCPAccessPolicyChanged
		}
		rules = append(rules, api.EdgeRuleResponse{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Kind: r.Kind, MatchHost: r.MatchHost, MatchPath: r.MatchPath, MatchMethods: r.MatchMethods, MatchHeaders: headers, Priority: int(r.Priority), Enabled: r.Enabled, Action: actionBytes, ExpiresAt: expires, Match: match, Mode: r.Mode})
	}
	evidence, err := q.ReadMCPGatewayPromotionEvidence(ctx, tx, mustPgUUID(guard.AccountID))
	if err != nil {
		return err
	}
	fingerprint, fingerprintErr := api.MCPAccessPolicyFingerprint(guard.Expectation.Resource, rules, evidence.CapturedAt.Time)
	if fingerprintErr != nil {
		return ErrMCPAccessPolicyChanged
	}
	if err = observeMCPAdmission(ctx, fingerprint, &evidence); err != nil {
		return err
	}
	if guard.Expectation.Validate() != nil || fingerprint != guard.Expectation.Fingerprint {
		return ErrMCPAccessPolicyChanged
	}
	if guard.Expectation.AccountRevision == nil {
		return nil
	}
	return checkMCPGatewayPromotionEvidence(*guard.Expectation.AccountRevision, evidence)
}
