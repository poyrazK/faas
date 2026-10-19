package state

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueCompoundPolicy(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, account string) (api.IssueCompoundAlertPolicy, error) {
	row, err := q.IssueCompoundPolicy(ctx, db, sqlc.IssueCompoundPolicyParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	if err != nil {
		return api.IssueCompoundAlertPolicy{}, mapErr(err)
	}
	out := api.IssueCompoundAlertPolicy{Revision: row.Revision, Rules: []api.IssueCompoundAlertRule{}}
	if err = json.Unmarshal(row.Rules, &out.Rules); err != nil {
		return out, err
	}
	return out, nil
}

func (s *PgStore) GetIssueCompoundAlertPolicy(ctx context.Context, app, account string) (api.IssueCompoundAlertPolicy, error) {
	return issueCompoundPolicy(ctx, sqlc.New(), s.pool, app, account)
}

func (s *PgStore) SetIssueCompoundAlertPolicy(ctx context.Context, app, account string, in api.UpdateIssueCompoundAlertPolicyRequest) (api.IssueCompoundAlertPolicy, error) {
	empty := api.IssueCompoundAlertPolicy{}
	if in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || in.Rules == nil {
		return empty, ErrInvalidArgument
	}
	rules, err := api.NormalizeIssueCompoundAlertRules(in.Rules)
	if err != nil {
		return empty, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return empty, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return empty, ErrNotFound
	}
	current, err := issueCompoundPolicy(ctx, q, tx, app, account)
	if err != nil {
		return empty, err
	}
	if current.Revision != *in.ExpectedRevision {
		return empty, ErrIssueCompoundAlertRevision
	}
	if reflect.DeepEqual(current.Rules, rules) {
		return current, nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return empty, err
	}
	out := api.IssueCompoundAlertPolicy{Revision: current.Revision + 1, Rules: rules}
	if err = q.IssueCompoundPutPolicy(ctx, tx, sqlc.IssueCompoundPutPolicyParams{AppID: owner.ID, Revision: out.Revision, Rules: raw}); err != nil {
		return empty, err
	}
	keys := make([]string, 0, len(rules))
	previous := make(map[string]string, len(current.Rules))
	for _, rule := range current.Rules {
		previous[rule.Key] = issues.CompoundRuleHash(rule)
	}
	for _, rule := range rules {
		if previous[rule.Key] == issues.CompoundRuleHash(rule) {
			keys = append(keys, rule.Key)
		}
	}
	if err = q.IssueCompoundPruneReceipts(ctx, tx, sqlc.IssueCompoundPruneReceiptsParams{AppID: owner.ID, Keys: keys}); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return out, nil
}
