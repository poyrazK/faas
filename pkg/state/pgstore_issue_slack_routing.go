package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func slackRoutingChanged() error {
	return api.NewProblem(409, "issue_slack_routing_changed", "Slack channel rules changed", "reload channel rules and review the current workspace before saving")
}
func readSlackRouting(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, owner string) (IssueSlackRoutingPolicy, error) {
	r, err := q.IssueSlackRoutingPolicy(ctx, db, sqlc.IssueSlackRoutingPolicyParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	out := IssueSlackRoutingPolicy{Revision: r.Revision, InstallationRevision: r.InstallationRevision, Active: r.Active, Rules: []IssueSlackRoutingRule{}}
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(r.Rules, &out.Rules)
	return out, err
}
func (s *PgStore) GetIssueSlackRoutingPolicy(ctx context.Context, app, owner string) (IssueSlackRoutingPolicy, error) {
	return readSlackRouting(ctx, sqlc.New(), s.pool, app, owner)
}
func checkSlackRoutingDraft(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, owner string, in PutIssueSlackRoutingPolicy) (IssueSlackRoutingPolicy, error) {
	empty := IssueSlackRoutingPolicy{}
	if err := slackOAuthOwnerReady(ctx, q, tx, app, owner); err != nil {
		return empty, err
	}
	current, err := readSlackRouting(ctx, q, tx, app, owner)
	if err != nil {
		return empty, err
	}
	install, err := readSlackInstallation(ctx, q, tx, app, owner)
	if err != nil {
		return empty, err
	}
	if in.Revision < 0 || in.Revision != current.Revision || install.Status != "connected" || in.InstallationRevision != install.Revision {
		return empty, slackRoutingChanged()
	}
	for _, r := range in.Rules {
		if r.TeamID != "" {
			exists, e := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: issueUUID(app), TeamID: issueUUID(r.TeamID)})
			if e != nil {
				return empty, e
			}
			if !exists {
				return empty, ErrInvalidArgument
			}
		}
	}
	return current, nil
}
func (s *PgStore) SetIssueSlackRoutingPolicy(ctx context.Context, app, owner string, in PutIssueSlackRoutingPolicy) (IssueSlackRoutingPolicy, error) {
	out := IssueSlackRoutingPolicy{}
	rules, err := NormalizeIssueSlackRoutingRules(in.Rules)
	if err != nil {
		return out, ErrInvalidArgument
	}
	in.Rules = rules
	err = s.slackOAuthTransaction(ctx, app, owner, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		current, e := checkSlackRoutingDraft(ctx, q, tx, app, owner, in)
		if e != nil {
			return e
		}
		out = IssueSlackRoutingPolicy{Revision: current.Revision, InstallationRevision: in.InstallationRevision, Active: true, Rules: rules}
		if current.Active && current.InstallationRevision == in.InstallationRevision && reflect.DeepEqual(current.Rules, rules) {
			return nil
		}
		out.Revision++
		raw, e := json.Marshal(rules)
		if e != nil {
			return e
		}
		if e = q.IssueSlackRoutingPut(ctx, tx, sqlc.IssueSlackRoutingPutParams{AppID: a.ID, AccountID: a.AccountID, Revision: out.Revision, InstallationRevision: in.InstallationRevision, Rules: raw}); e != nil {
			return e
		}
		if e = q.IssueSlackRoutingRelease(ctx, tx, sqlc.IssueSlackRoutingReleaseParams{AppID: a.ID, RoutingRevision: out.Revision}); e != nil {
			return e
		}
		return q.IssueSlackRoutingCancel(ctx, tx, sqlc.IssueSlackRoutingCancelParams{AppID: a.ID, RoutingRevision: out.Revision})
	})
	return out, err
}
func (s *PgStore) PreviewIssueSlackRouting(ctx context.Context, app, owner string, in PutIssueSlackRoutingPolicy, sample IssueSlackRoutingSample) (IssueSlackRoutingPreview, error) {
	out := IssueSlackRoutingPreview{Outcome: "unrouted", Revision: in.Revision, InstallationRevision: in.InstallationRevision}
	rules, err := NormalizeIssueSlackRoutingRules(in.Rules)
	if err != nil || !ApplicationSlackEvent(sample.Event) || !ValidSlackRoutingEnvironment(sample.Environment) || !validSlackThresholdRuleKey(sample.Event, sample.FrequencyRuleKey) {
		return out, ErrInvalidArgument
	}
	in.Rules = rules
	err = s.slackOAuthTransaction(ctx, app, owner, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		if _, e := checkSlackRoutingDraft(ctx, q, tx, app, owner, in); e != nil {
			return e
		}
		if sample.AssignedTeamID != "" {
			exists, e := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: a.ID, TeamID: issueUUID(sample.AssignedTeamID)})
			if e != nil {
				return e
			}
			if !exists {
				return ErrInvalidArgument
			}
		}
		policy, e := issueApplicationSlackPolicy(ctx, q, tx, app, owner)
		if e != nil {
			return e
		}
		if !policy.Enabled || !slices.Contains(policy.Events, sample.Event) {
			out.Outcome = "alerts_disabled"
			return nil
		}
		route, e := q.IssueApplicationSlackRoute(ctx, tx, sqlc.IssueApplicationSlackRouteParams{AppID: a.ID, AccountID: a.AccountID, AssignedTeamID: issueUUID(sample.AssignedTeamID), FallbackTeamID: issueUUID(policy.FallbackTeamID)})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		out.Outcome = "team_default"
		out.TeamID = issueID(route.TeamID)
		out.TeamName = route.TeamName
		out.ChannelID = route.ChannelID
		out.DefaultChannelID = route.ChannelID
		if route.OauthRevision.Valid && route.OauthRevision.Int64 == in.InstallationRevision {
			out.RuleKey, out.ChannelID = MatchIssueSlackRouting(rules, sample.Event, sample.Environment, sample.FrequencyRuleKey, out.TeamID)
			if out.RuleKey != "" {
				out.Outcome = "rule"
			} else {
				out.ChannelID = route.ChannelID
			}
		}
		return nil
	})
	return out, err
}
