package state

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueFrequencyPolicy(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, account string) (api.IssueFrequencyAlertPolicy, error) {
	row, err := q.IssueFrequencyPolicy(ctx, db, sqlc.IssueFrequencyPolicyParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	if err != nil {
		return api.IssueFrequencyAlertPolicy{}, mapErr(err)
	}
	out := api.IssueFrequencyAlertPolicy{Revision: row.Revision, Rules: []api.IssueFrequencyAlertRule{}}
	if err = json.Unmarshal(row.Rules, &out.Rules); err != nil {
		return out, err
	}
	return out, nil
}

func (s *PgStore) GetIssueFrequencyAlertPolicy(ctx context.Context, app, account string) (api.IssueFrequencyAlertPolicy, error) {
	return issueFrequencyPolicy(ctx, sqlc.New(), s.pool, app, account)
}

func (s *PgStore) SetIssueFrequencyAlertPolicy(ctx context.Context, app, account string, in api.UpdateIssueFrequencyAlertPolicyRequest) (api.IssueFrequencyAlertPolicy, error) {
	empty := api.IssueFrequencyAlertPolicy{}
	if in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || in.Rules == nil {
		return empty, ErrInvalidArgument
	}
	rules, err := api.NormalizeIssueFrequencyAlertRules(in.Rules)
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
	current, err := issueFrequencyPolicy(ctx, q, tx, app, account)
	if err != nil {
		return empty, err
	}
	if current.Revision != *in.ExpectedRevision {
		return empty, ErrIssueFrequencyAlertRevision
	}
	if reflect.DeepEqual(current.Rules, rules) {
		return current, nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return empty, err
	}
	out := api.IssueFrequencyAlertPolicy{Revision: current.Revision + 1, Rules: rules}
	if err = q.IssueFrequencyPutPolicy(ctx, tx, sqlc.IssueFrequencyPutPolicyParams{AppID: owner.ID, Revision: out.Revision, Rules: raw}); err != nil {
		return empty, err
	}
	keys := make([]string, 0, len(rules))
	previous := make(map[string]string, len(current.Rules))
	for _, rule := range current.Rules {
		previous[rule.Key] = issues.FrequencyRuleHash(rule)
	}
	for _, rule := range rules {
		if previous[rule.Key] == issues.FrequencyRuleHash(rule) {
			keys = append(keys, rule.Key)
		}
	}
	if err = q.IssueFrequencyPruneReceipts(ctx, tx, sqlc.IssueFrequencyPruneReceiptsParams{AppID: owner.ID, Keys: keys}); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return out, nil
}
