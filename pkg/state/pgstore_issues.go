package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueUUID(id string) pgtype.UUID {
	v, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: v, Valid: err == nil}
}
func issueID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func issueTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}
func issueTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
func issueNullableTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return issueTime(*t)
}
func issueRow(r sqlc.AppIssue) api.Issue {
	return api.Issue{SplitSourceIssueID: issueID(r.SplitSourceIssueID), ID: issueID(r.ID), AppID: issueID(r.AppID), Environment: r.Environment, Fingerprint: r.Fingerprint, GroupingVersion: int(r.GroupingVersion), Title: r.Title, State: r.State, AssigneeAccountID: issueID(r.AssigneeAccountID), AssigneeTeamID: issueID(r.AssigneeTeamID), FirstSeenAt: r.FirstSeenAt.Time, LastSeenAt: r.LastSeenAt.Time, EventCount: r.EventCount, RegressionCount: r.RegressionCount, ResolvedAt: issueTimePtr(r.ResolvedAt), FixedDeploymentID: issueID(r.FixedDeploymentID), FixedDeploymentCreatedAt: issueTimePtr(r.FixedDeploymentCreatedAt), IgnoredUntil: issueTimePtr(r.IgnoredUntil)}
}
func issueImpactListRow(r sqlc.IssueListByImpactRow) api.Issue {
	return api.Issue{
		SplitSourceIssueID: issueID(r.SplitSourceIssueID), ID: issueID(r.ID), AppID: issueID(r.AppID), Environment: r.Environment, Fingerprint: r.Fingerprint,
		GroupingVersion: int(r.GroupingVersion), Title: r.Title, State: r.State,
		AssigneeAccountID: issueID(r.AssigneeAccountID), AssigneeTeamID: issueID(r.AssigneeTeamID), FirstSeenAt: r.FirstSeenAt.Time, LastSeenAt: r.LastSeenAt.Time,
		EventCount: r.EventCount, RegressionCount: r.RegressionCount, ResolvedAt: issueTimePtr(r.ResolvedAt),
		FixedDeploymentID: issueID(r.FixedDeploymentID), FixedDeploymentCreatedAt: issueTimePtr(r.FixedDeploymentCreatedAt),
		IgnoredUntil: issueTimePtr(r.IgnoredUntil),
		Impact24h: &api.IssueImpactSummary{
			IdentifiedCustomers: r.IdentifiedCustomers,
			ObservedEvents:      r.ObservedEvents,
			UnattributedEvents:  r.UnattributedEvents,
			UnknownHTTPBatches:  r.UnknownHttpBatches,
		},
	}
}
func issueTokenRow(r sqlc.IssueIngestToken) api.IssueIngestToken {
	return api.IssueIngestToken{ID: issueID(r.ID), AppID: issueID(r.AppID), DeploymentID: issueID(r.DeploymentID), Environment: r.Environment, Name: r.Name, ExpiresAt: r.ExpiresAt.Time, RevokedAt: issueTimePtr(r.RevokedAt)}
}

func (s *PgStore) CreateIssueToken(ctx context.Context, account, app string, in api.CreateIssueIngestTokenRequest, hash []byte, lim api.IssueLimits) (api.IssueIngestToken, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueIngestToken{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueIngestToken{}, ErrNotFound
	}
	if _, err = q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{DeploymentID: issueUUID(in.DeploymentID), AppID: issueUUID(app), AccountID: issueUUID(account), Environment: in.Environment}); err != nil {
		return api.IssueIngestToken{}, mapErr(err)
	}
	n, err := q.IssueCountTokens(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	if n >= int64(lim.TokensPerApp) {
		return api.IssueIngestToken{}, &IssueLimitError{Resource: "ingest_tokens", Limit: int64(lim.TokensPerApp), Observed: n + 1}
	}
	r, err := q.IssueInsertToken(ctx, tx, sqlc.IssueInsertTokenParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(in.DeploymentID), Environment: in.Environment, Name: in.Name, TokenHash: storedCredential(credTagIssueIngestToken, hash), ExpiresAt: issueTime(in.ExpiresAt)})
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	return issueTokenRow(r), tx.Commit(ctx)
}
func (s *PgStore) FindIssueToken(ctx context.Context, hash []byte, now time.Time) (IssueCredential, error) {
	r, stored, err := firstCredentialMatch(ctx, credentialLookups(credTagIssueIngestToken, hash), func(ctx context.Context, value []byte) (sqlc.IssueIngestToken, error) {
		r, err := sqlc.New().IssueFindToken(ctx, s.pool, sqlc.IssueFindTokenParams{TokenHash: value, Now: issueTime(now)})
		return r, mapErr(err)
	})
	if err != nil {
		return IssueCredential{}, err
	}
	// ADR 20261029202403: rewrite a plain or older-key hash on use.
	if next, stale := staleCredential(credTagIssueIngestToken, hash, stored); stale {
		_, _ = s.pool.Exec(ctx, `update issue_ingest_tokens set token_hash = $1 where id = $2 and token_hash = $3`, next, r.ID, stored)
	}
	return IssueCredential{issueID(r.ID), issueID(r.AccountID), issueID(r.AppID), issueID(r.DeploymentID), r.Environment}, nil
}
func (s *PgStore) ListIssueTokens(ctx context.Context, app string) ([]api.IssueIngestToken, error) {
	rows, err := sqlc.New().IssueListTokens(ctx, s.pool, issueUUID(app))
	out := make([]api.IssueIngestToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueTokenRow(r))
	}
	return out, err
}
func (s *PgStore) RevokeIssueToken(ctx context.Context, app, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return mapErr(err)
	}
	n, err := q.IssueRevokeToken(ctx, tx, sqlc.IssueRevokeTokenParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// RecordIssue serializes short writes per app. The unique event key, group
// creation, counters, attribution, and transition outbox commit together.
func (s *PgStore) RecordIssue(ctx context.Context, in RecordIssueParams) (api.IssueEventResponse, error) {
	results, err := s.RecordIssueBatch(ctx, []RecordIssueParams{in})
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	return results[0], nil
}

// RecordIssueBatch commits all occurrences together, so a failed export does
// not leave an accepted prefix behind. The app lock serializes quota checks.
func (s *PgStore) RecordIssueBatch(ctx context.Context, inputs []RecordIssueParams) ([]api.IssueEventResponse, error) {
	if len(inputs) > api.IssueMaxBatchEvents {
		return nil, ErrInvalidArgument
	}
	for _, in := range inputs {
		if in.Credential != inputs[0].Credential || in.Limits != inputs[0].Limits || in.EnforceResourcePolicy != inputs[0].EnforceResourcePolicy {
			return nil, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(inputs) > 0 && inputs[0].EnforceResourcePolicy {
		limits, err := lockIssueResourcePolicy(ctx, tx, inputs[0].Credential)
		if err != nil {
			return nil, err
		}
		inputs = append([]RecordIssueParams(nil), inputs...)
		retentionFloor := time.Now().UTC().AddDate(0, 0, -limits.RetentionDays)
		for i := range inputs {
			inputs[i].Limits = limits
			if inputs[i].Event.OccurredAt.Before(retentionFloor) {
				return nil, ErrInvalidArgument
			}
		}
	}
	results := make([]api.IssueEventResponse, 0, len(inputs))
	batchMaps := make(map[string]*issues.SourceMapSet)
	for _, in := range inputs {
		out, err := s.recordIssueTx(ctx, tx, in, batchMaps)
		if err != nil {
			var limit *IssueLimitError
			if errors.As(err, &limit) && (limit.Rate || limit.Resource == "events") {
				limit.NoCapacityAtBatchStart = true
				for _, prior := range results {
					if !prior.Duplicate {
						limit.NoCapacityAtBatchStart = false
						break
					}
				}
				// Bounded retention cleanup must make progress even if the app
				// remains full. An atomic batch with no new occurrence has only
				// performed duplicate reads and retention bookkeeping so far.
				if limit.Resource == "events" && limit.NoCapacityAtBatchStart {
					if commitErr := tx.Commit(ctx); commitErr != nil {
						return nil, commitErr
					}
				}
			}
			return nil, err
		}
		results = append(results, out)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

// Lock the financial owner before the app, matching plan-update lock order.
// This prevents an in-flight capture from committing after a suspension or
// downgrade using an earlier policy snapshot.
func lockIssueResourcePolicy(ctx context.Context, tx pgx.Tx, credential IssueCredential) (api.IssueLimits, error) {
	q := sqlc.New()
	app, err := q.IssuePolicyApp(ctx, tx, issueUUID(credential.AppID))
	if err != nil {
		return api.IssueLimits{}, mapErr(err)
	}
	if issueID(app.AccountID) != credential.AccountID {
		return api.IssueLimits{}, ErrNotFound
	}
	account, err := q.IssuePolicyLockAccount(ctx, tx, app.AccountID)
	if err != nil {
		return api.IssueLimits{}, mapErr(err)
	}
	policy := Account{Plan: api.Plan(account.Plan), Status: AccountStatus(account.Status), AbuseHoldAt: issueTimePtr(account.AbuseHoldAt)}
	if app.OrgID.Valid {
		org, orgErr := q.IssuePolicyLockOrg(ctx, tx, app.OrgID)
		if orgErr != nil {
			return api.IssueLimits{}, mapErr(orgErr)
		}
		if org.PersonalOrg {
			if org.PersonalOwnerAccountID != app.AccountID {
				return api.IssueLimits{}, ErrConflict
			}
		} else if org.ResourceOwnershipEnabled {
			policy = Account{Plan: api.Plan(org.Plan), Status: AccountStatus(org.Status), AbuseHoldAt: issueTimePtr(org.AbuseHoldAt)}
			if org.DeletedPending {
				policy.Status = AccountDeletedPending
			}
		} else {
			policy.Status = orgLegacyOwnerStatus(Org{Status: OrgStatus(org.Status), DeletedPending: org.DeletedPending}, policy.Status)
			if org.AbuseHoldAt.Valid {
				policy.AbuseHoldAt = issueTimePtr(org.AbuseHoldAt)
			}
		}
	}
	locked, err := q.IssueLockApp(ctx, tx, issueUUID(credential.AppID))
	if err != nil {
		return api.IssueLimits{}, mapErr(err)
	}
	if locked.AccountID != app.AccountID || locked.OrgID != app.OrgID {
		return api.IssueLimits{}, ErrConflict
	}
	limits := policy.Plan.IssueLimits()
	if !policy.Active() || !limits.Enabled {
		return api.IssueLimits{}, ErrIssuePolicyChanged
	}
	return limits, nil
}

func validateIssueTimeBuckets(in RecordIssueParams, count int64) error {
	if len(in.TimeBuckets) == 0 {
		return nil
	}
	if len(in.TimeBuckets) > 1000 || in.Credential.ID != "" || in.Event.SourceKind != "http" {
		return ErrInvalidArgument
	}
	second := in.Event.OccurredAt.UnixMilli() / 1000
	sample := in.Event.OccurredAt.UnixMilli()
	var sum, previous int64
	found := false
	for i, pair := range in.TimeBuckets {
		if pair[0]/1000 != second || pair[1] <= 0 || (i > 0 && pair[0] <= previous) || pair[0] > in.Now.UnixMilli() {
			return ErrInvalidArgument
		}
		previous = pair[0]
		sum += pair[1]
		if sum > count {
			return ErrInvalidArgument
		}
		if pair[0] == sample {
			found = true
		}
	}
	if sum != count || !found {
		return ErrInvalidArgument
	}
	return nil
}

func issueEarliestEventTime(in RecordIssueParams) time.Time {
	earliest := in.Event.OccurredAt
	if len(in.TimeBuckets) > 0 {
		at := time.UnixMilli(in.TimeBuckets[0][0]).UTC()
		if at.Before(earliest) {
			earliest = at
		}
	}
	return earliest
}

func issueLatestEventTime(in RecordIssueParams) time.Time {
	latest := in.Event.OccurredAt
	if len(in.TimeBuckets) > 0 {
		at := time.UnixMilli(in.TimeBuckets[len(in.TimeBuckets)-1][0]).UTC()
		if at.After(latest) {
			latest = at
		}
	}
	return latest
}

func (s *PgStore) recordIssueTx(ctx context.Context, tx pgx.Tx, in RecordIssueParams, batchMaps map[string]*issues.SourceMapSet) (api.IssueEventResponse, error) {
	q := sqlc.New()
	c := in.Credential
	count := max(int64(1), in.OccurrenceCount)
	if count > api.IssueHTTPBatchMaxCount || (count > 1 && (c.ID != "" || in.Event.SourceKind != "http")) {
		return api.IssueEventResponse{}, ErrInvalidArgument
	}
	if err := validateIssueTimeBuckets(in, count); err != nil {
		return api.IssueEventResponse{}, err
	}
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(c.AppID))
	if err != nil {
		return api.IssueEventResponse{}, mapErr(err)
	}
	if issueID(owner.AccountID) != c.AccountID {
		return api.IssueEventResponse{}, ErrNotFound
	}
	if c.ID != "" {
		valid, checkErr := q.IssueTokenStillValid(ctx, tx, sqlc.IssueTokenStillValidParams{ID: issueUUID(c.ID), AppID: owner.ID, DeploymentID: issueUUID(c.DeploymentID), Now: issueTime(time.Now().UTC())})
		if checkErr != nil {
			return api.IssueEventResponse{}, checkErr
		}
		if !valid {
			return api.IssueEventResponse{}, ErrNotFound
		}
	}
	dep, err := q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{DeploymentID: issueUUID(c.DeploymentID), AppID: issueUUID(c.AppID), AccountID: owner.AccountID, Environment: c.Environment})
	if err != nil {
		return api.IssueEventResponse{}, mapErr(err)
	}
	old, err := q.IssueFindEvent(ctx, tx, sqlc.IssueFindEventParams{AppID: owner.ID, DeploymentID: dep.ID, EventID: issueUUID(in.Event.EventID)})
	if err == nil {
		if old.PayloadHash != in.PayloadHash {
			return api.IssueEventResponse{}, ErrIssueEventConflict
		}
		canonical, resolveErr := q.IssueCanonicalID(ctx, tx, sqlc.IssueCanonicalIDParams{AppID: owner.ID, ID: old.IssueID})
		return api.IssueEventResponse{IssueID: issueID(canonical), EventID: in.Event.EventID, Duplicate: true}, resolveErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.IssueEventResponse{}, err
	}
	// The app lock also serializes artifact publication/retirement. Exact retries
	// above keep their original evidence even if mapping artifacts are retired.
	if err = symbolicateIssueTx(ctx, q, tx, &in, batchMaps); err != nil {
		return api.IssueEventResponse{}, err
	}
	if _, err = q.IssuePurgeEventsBounded(ctx, tx, sqlc.IssuePurgeEventsBoundedParams{AppID: owner.ID, Before: issueTime(in.Now.AddDate(0, 0, -in.Limits.RetentionDays)), BatchLimit: api.IssueIngestPurgeBatch}); err != nil {
		return api.IssueEventResponse{}, err
	}
	if err = q.IssueCreateEventUsage(ctx, tx, owner.ID); err != nil {
		return api.IssueEventResponse{}, err
	}
	usage, err := q.IssueLockEventUsage(ctx, tx, owner.ID)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	n := usage.RetainedCount
	if !usage.Initialized {
		n, err = q.IssueCountEvents(ctx, tx, owner.ID)
		if err != nil {
			return api.IssueEventResponse{}, err
		}
		if err = q.IssueInitializeEventUsage(ctx, tx, sqlc.IssueInitializeEventUsageParams{AppID: owner.ID, RetainedCount: n}); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	if n >= int64(in.Limits.EventsPerApp) {
		return api.IssueEventResponse{}, &IssueLimitError{Resource: "events", Limit: int64(in.Limits.EventsPerApp), Observed: n + 1}
	}
	n, err = q.IssueCountRecentEvents(ctx, tx, sqlc.IssueCountRecentEventsParams{AppID: owner.ID, Since: issueTime(in.Now.Add(-time.Minute))})
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if n >= int64(in.Limits.EventsPerMinute) {
		return api.IssueEventResponse{}, &IssueLimitError{Resource: "events_per_minute", Limit: int64(in.Limits.EventsPerMinute), Observed: n + 1, Rate: true}
	}
	row, err := q.IssueFindGroup(ctx, tx, sqlc.IssueFindGroupParams{AppID: owner.ID, Environment: c.Environment, GroupingVersion: int32(in.GroupingVersion), Fingerprint: in.Fingerprint})
	created := errors.Is(err, pgx.ErrNoRows)
	if created {
		n, err = q.IssueCount(ctx, tx, owner.ID)
		if err != nil {
			return api.IssueEventResponse{}, err
		}
		if n >= int64(in.Limits.IssuesPerApp) {
			return api.IssueEventResponse{}, &IssueLimitError{Resource: "issues", Limit: int64(in.Limits.IssuesPerApp), Observed: n + 1}
		}
		row, err = q.IssueCreate(ctx, tx, sqlc.IssueCreateParams{AccountID: owner.AccountID, AppID: owner.ID, Environment: c.Environment, Fingerprint: in.Fingerprint, GroupingVersion: int32(in.GroupingVersion), Title: in.Title, OccurredAt: issueTime(issueEarliestEventTime(in))})
	}
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	provenanceID := row.ID
	row, err = issueRouteSplitCapture(ctx, q, tx, row, in.Event)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	originalID := row.ID
	group, err := issueCanonicalRow(ctx, q, tx, c.AppID, issueID(originalID))
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	consumer, tenant, err := issueAttribution(ctx, q, tx, in)
	if errors.Is(err, ErrIssueAttributionConflict) {
		consumer, tenant = pgtype.UUID{}, pgtype.UUID{}
	} else if err != nil {
		return api.IssueEventResponse{}, err
	}
	policy, err := q.IssueGetImpactAlertPolicy(ctx, tx, owner.ID)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	minimumCustomers := int64(policy)
	var customersBefore int64
	if minimumCustomers > 0 {
		customersBefore, err = q.IssueImpactCustomerCount(ctx, tx, sqlc.IssueImpactCustomerCountParams{
			IssueID: group.ID, Since: issueTime(in.Now.Add(-api.IssueImpactAlertWindow)), Until: issueTime(in.Now),
		})
		if err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	raw, err := json.Marshal(in.Event)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	// The ingress body is capped at 64 KiB; normalization and source maps
	// can expand it. Keep a separate bound before PostgreSQL's JSONB check.
	if len(raw) > 512<<10 {
		return api.IssueEventResponse{}, ErrInvalidArgument
	}
	bucketJSON := []byte("[]")
	if len(in.TimeBuckets) != 0 {
		bucketJSON, err = json.Marshal(in.TimeBuckets)
		if err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	if err = q.IssueInsertEvent(ctx, tx, sqlc.IssueInsertEventParams{AppID: owner.ID, DeploymentID: dep.ID, EventID: issueUUID(in.Event.EventID), IssueID: row.ID, OriginalIssueID: provenanceID, PayloadHash: in.PayloadHash, Payload: raw, OccurredAt: issueTime(in.Event.OccurredAt), ReceivedAt: issueTime(in.Now), ConsumerID: consumer, TenantID: tenant, OccurrenceCount: count, HttpTimeBuckets: bucketJSON}); err != nil {
		return api.IssueEventResponse{}, err
	}
	regressed := issues.Recurs(issueRow(group), c.DeploymentID, issueLatestEventTime(in), group.FixedDeploymentCreatedAt.Valid && dep.CreatedAt.Time.After(group.FixedDeploymentCreatedAt.Time))
	row, err = q.IssueObserve(ctx, tx, sqlc.IssueObserveParams{ID: row.ID, OccurredAt: issueTime(issueEarliestEventTime(in)), LastOccurredAt: issueTime(issueLatestEventTime(in)), Regressed: regressed, OccurrenceCount: count})
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	var autoAssigned *api.IssueOwnershipRule
	if created {
		rules, listErr := q.IssueListOwnershipRules(ctx, tx, owner.ID)
		if listErr != nil {
			return api.IssueEventResponse{}, listErr
		}
		for _, stored := range rules {
			rule := api.IssueOwnershipRule{
				SourcePathPrefix: stored.SourcePathPrefix.String, Tag: stored.TagFilter.String,
				ExceptionType:     stored.ExceptionType.String,
				SourceKind:        stored.SourceKind.String,
				RoutePrefix:       stored.RoutePrefix.String,
				AssigneeAccountID: issueID(stored.AssigneeAccountID), AssigneeTeamID: issueID(stored.AssigneeTeamID),
			}
			if !issueOwnershipRuleMatches(rule, in.Event) {
				continue
			}
			allowed, allowedErr := issueAssignmentAllowed(ctx, q, tx, owner.ID, stored.AssigneeAccountID, stored.AssigneeTeamID)
			if allowedErr != nil {
				return api.IssueEventResponse{}, allowedErr
			}
			if allowed {
				_, err = q.IssueSetNewIssueAssignee(ctx, tx, sqlc.IssueSetNewIssueAssigneeParams{ID: row.ID, AssigneeAccountID: stored.AssigneeAccountID, AssigneeTeamID: stored.AssigneeTeamID})
				if err != nil {
					return api.IssueEventResponse{}, err
				}
				autoAssigned = &rule
			}
			// The first matching rule is authoritative. A target whose org
			// membership was revoked since configuration leaves the issue open
			// and unassigned; later rules do not silently take its place.
			break
		}
	}
	if err = q.IssueObserveRelease(ctx, tx, sqlc.IssueObserveReleaseParams{IssueID: originalID, DeploymentID: dep.ID, CommitSha: dep.CommitSha.String, ImageDigest: dep.ImageDigest, OccurredAt: issueTime(issueEarliestEventTime(in)), LastOccurredAt: issueTime(issueLatestEventTime(in)), OccurrenceCount: count}); err != nil {
		return api.IssueEventResponse{}, err
	}
	if regressed {
		if err = q.IssueReopenGroup(ctx, tx, group.ID); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	row, err = issueGroupRow(ctx, q, tx, group)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if autoAssigned != nil {
		details := map[string]string{"assignee_account_id": autoAssigned.AssigneeAccountID, "assignee_team_id": autoAssigned.AssigneeTeamID, "assignment_source": "ownership_rule"}
		if autoAssigned.ExceptionType != "" {
			details["exception_type"] = autoAssigned.ExceptionType
		}
		if autoAssigned.SourceKind != "" {
			details["source_kind"] = autoAssigned.SourceKind
		}
		if autoAssigned.SourcePathPrefix != "" {
			details["source_path_prefix"] = autoAssigned.SourcePathPrefix
		}
		if autoAssigned.Tag != "" {
			details["tag"] = autoAssigned.Tag
		}
		if autoAssigned.RoutePrefix != "" {
			details["route_prefix"] = autoAssigned.RoutePrefix
		}
		if err = issueActivity(ctx, q, tx, row, "assigned", "", details, in.Now); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	if minimumCustomers > 0 {
		customersAfter, countErr := q.IssueImpactCustomerCount(ctx, tx, sqlc.IssueImpactCustomerCountParams{
			IssueID: row.ID, Since: issueTime(in.Now.Add(-api.IssueImpactAlertWindow)), Until: issueTime(in.Now),
		})
		if countErr != nil {
			return api.IssueEventResponse{}, countErr
		}
		if err = recordIssueImpactThresholdTransition(ctx, q, tx, row, minimumCustomers, customersBefore, customersAfter, c.DeploymentID, in.Now); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	if err = recordIssueFrequencyAlerts(ctx, q, tx, row, in); err != nil {
		return api.IssueEventResponse{}, err
	}
	action := ""
	if created {
		action = "created"
	}
	if regressed {
		action = "regressed"
	}
	if action != "" {
		if err = issueActivity(ctx, q, tx, row, action, "", map[string]string{"deployment_id": c.DeploymentID}, in.Now); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	// Regression activity resets response state before evaluating a new
	// incident, so a prior incident's snooze cannot suppress this alert.
	if err = recordIssueCompoundAlerts(ctx, q, tx, row, in); err != nil {
		return api.IssueEventResponse{}, err
	}
	return api.IssueEventResponse{IssueID: issueID(row.ID), EventID: in.Event.EventID, Regressed: regressed}, nil
}

func issueAttribution(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, in RecordIssueParams) (pgtype.UUID, pgtype.UUID, error) {
	var consumer, tenant pgtype.UUID
	c := in.Credential
	if in.Event.InvocationID != "" {
		r, err := q.IssueInvocationScope(ctx, db, sqlc.IssueInvocationScopeParams{ID: issueUUID(in.Event.InvocationID), AppID: issueUUID(c.AppID), AccountID: issueUUID(c.AccountID)})
		if err != nil {
			return consumer, tenant, mapErr(err)
		}
		tenant = r.PlatformTenantID
	}
	if in.Event.RequestID != "" {
		rows, err := q.IssueRequestAttribution(ctx, db, sqlc.IssueRequestAttributionParams{AccountID: issueUUID(c.AccountID), AppID: issueUUID(c.AppID), DeploymentID: issueUUID(c.DeploymentID), RequestID: in.Event.RequestID, Since: issueTime(in.Event.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(in.Event.OccurredAt.Add(api.IssueMaxClockSkew))})
		if err != nil {
			return consumer, tenant, err
		}
		if len(rows) > 1 {
			return consumer, tenant, ErrIssueAttributionConflict
		}
		if len(rows) == 1 {
			if in.Event.TraceID != "" && rows[0].TraceID != in.Event.TraceID {
				return consumer, tenant, ErrIssueAttributionConflict
			}
			consumer = issueUUID(rows[0].ConsumerKey)
			if tenant.Valid && rows[0].PlatformTenantID.Valid && tenant != rows[0].PlatformTenantID {
				return consumer, tenant, ErrIssueAttributionConflict
			}
			if rows[0].PlatformTenantID.Valid {
				tenant = rows[0].PlatformTenantID
			}
			// Anonymous audit evidence is an affirmative observation. Do not
			// promote it using an independently supplied correlation hint.
			if !consumer.Valid && !tenant.Valid {
				return consumer, tenant, nil
			}
		}
	}
	if in.Event.RequestID != "" && in.Event.TraceID != "" {
		rows, err := q.IssueRequestTraceJournal(ctx, db, sqlc.IssueRequestTraceJournalParams{AccountID: issueUUID(c.AccountID), AppID: issueUUID(c.AppID), RequestID: in.Event.RequestID, Since: issueTime(in.Event.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(in.Event.OccurredAt.Add(api.IssueMaxClockSkew))})
		if err != nil {
			return consumer, tenant, err
		}
		if len(rows) > 1 || (len(rows) == 1 && rows[0].Valid && rows[0].String != in.Event.TraceID) {
			return consumer, tenant, ErrIssueAttributionConflict
		}
	}
	if in.Event.TraceID != "" {
		rows, err := q.IssueAttribution(ctx, db, sqlc.IssueAttributionParams{AccountID: issueUUID(c.AccountID), AppID: issueUUID(c.AppID), DeploymentID: issueUUID(c.DeploymentID), TraceID: pgtype.Text{String: in.Event.TraceID, Valid: true}, Since: issueTime(in.Event.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(in.Event.OccurredAt.Add(api.IssueMaxClockSkew))})
		if err != nil {
			return consumer, tenant, err
		}
		if len(rows) > 1 {
			return consumer, tenant, ErrIssueAttributionConflict
		}
		if len(rows) == 1 {
			// Fill a missing component only if the trace also corroborates
			// every previously verified component.
			if consumer.Valid && rows[0].ConsumerID.Valid && consumer != rows[0].ConsumerID {
				return consumer, tenant, ErrIssueAttributionConflict
			}
			if tenant.Valid && rows[0].PlatformTenantID.Valid && tenant != rows[0].PlatformTenantID {
				return consumer, tenant, ErrIssueAttributionConflict
			}
			if consumer.Valid && !rows[0].ConsumerID.Valid || tenant.Valid && !rows[0].PlatformTenantID.Valid {
				return consumer, tenant, nil
			}
			if rows[0].ConsumerID.Valid {
				consumer = rows[0].ConsumerID
			}
			if rows[0].PlatformTenantID.Valid {
				tenant = rows[0].PlatformTenantID
			}
		}
	}
	return consumer, tenant, nil
}

func issueActivity(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, action, actor string, details map[string]string, now time.Time) error {
	if action == "regressed" || action == "reopened" {
		if err := resetApplicationTriage(ctx, q, tx, row, actor); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	activity, err := q.IssueAddActivity(ctx, tx, sqlc.IssueAddActivityParams{IssueID: row.ID, Action: action, ActorAccountID: issueUUID(actor), CreatedAt: issueTime(now), Details: raw})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"id": issueID(activity.ID), "type": "issue." + action, "issue": issueRow(row), "details": details})
	if err != nil {
		return err
	}
	if err = enqueueApplicationIssueSlack(ctx, q, tx, row, activity.ID, action, details, now); err != nil {
		return err
	}
	return q.IssueAddTransition(ctx, tx, sqlc.IssueAddTransitionParams{ActivityID: activity.ID, AccountID: row.AccountID, AppID: row.AppID, Payload: payload})
}

func recordIssueImpactThresholdTransition(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, minimumCustomers, before, after int64, deploymentID string, now time.Time) error {
	if !issues.ImpactAlertCrossesThreshold(issueRow(row), minimumCustomers, before, after, now) {
		return nil
	}
	details := map[string]string{
		"minimum_customers":    fmt.Sprint(minimumCustomers),
		"identified_customers": fmt.Sprint(after),
		"window_seconds":       fmt.Sprint(int64(api.IssueImpactAlertWindow / time.Second)),
		"deployment_id":        deploymentID,
		"window_start":         now.Add(-api.IssueImpactAlertWindow).UTC().Format(time.RFC3339),
		"window_end":           now.UTC().Format(time.RFC3339),
	}
	return issueActivity(ctx, q, tx, row, "impact_threshold_reached", "", details, now)
}

func issueOwnershipRuleMatches(rule api.IssueOwnershipRule, event api.IssueEvent) bool {
	return issues.OwnershipRuleMatches(rule, event)
}

func (s *PgStore) GetIssueOwnershipRules(ctx context.Context, app string) (api.IssueOwnershipRules, error) {
	rows, err := sqlc.New().IssueListOwnershipRules(ctx, s.pool, issueUUID(app))
	if err != nil {
		return api.IssueOwnershipRules{}, err
	}
	out := api.IssueOwnershipRules{Rules: make([]api.IssueOwnershipRule, 0, len(rows))}
	for _, row := range rows {
		out.Rules = append(out.Rules, api.IssueOwnershipRule{
			SourcePathPrefix: row.SourcePathPrefix.String, Tag: row.TagFilter.String,
			ExceptionType: row.ExceptionType.String, SourceKind: row.SourceKind.String,
			RoutePrefix: row.RoutePrefix.String, AssigneeAccountID: issueID(row.AssigneeAccountID), AssigneeTeamID: issueID(row.AssigneeTeamID),
		})
	}
	return out, nil
}

func (s *PgStore) SetIssueOwnershipRules(ctx context.Context, app, account string, rules []api.IssueOwnershipRule) (api.IssueOwnershipRules, error) {
	rules, err := api.NormalizeIssueOwnershipRules(rules)
	if err != nil {
		return api.IssueOwnershipRules{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueOwnershipRules{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueOwnershipRules{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueOwnershipRules{}, ErrNotFound
	}
	for _, rule := range rules {
		allowed, checkErr := issueAssignmentAllowed(ctx, q, tx, owner.ID, issueUUID(rule.AssigneeAccountID), issueUUID(rule.AssigneeTeamID))
		if checkErr != nil {
			return api.IssueOwnershipRules{}, checkErr
		}
		if !allowed {
			return api.IssueOwnershipRules{}, ErrInvalidArgument
		}
	}
	if err = q.IssueDeleteOwnershipRules(ctx, tx, owner.ID); err != nil {
		return api.IssueOwnershipRules{}, err
	}
	for i, rule := range rules {
		if err = q.IssueInsertOwnershipRule(ctx, tx, sqlc.IssueInsertOwnershipRuleParams{
			AppID: owner.ID, RuleOrder: int32(i),
			SourcePathPrefix:  pgtype.Text{String: rule.SourcePathPrefix, Valid: rule.SourcePathPrefix != ""},
			TagFilter:         pgtype.Text{String: rule.Tag, Valid: rule.Tag != ""},
			ExceptionType:     pgtype.Text{String: rule.ExceptionType, Valid: rule.ExceptionType != ""},
			SourceKind:        pgtype.Text{String: rule.SourceKind, Valid: rule.SourceKind != ""},
			RoutePrefix:       pgtype.Text{String: rule.RoutePrefix, Valid: rule.RoutePrefix != ""},
			AssigneeAccountID: issueUUID(rule.AssigneeAccountID), AssigneeTeamID: issueUUID(rule.AssigneeTeamID),
		}); err != nil {
			return api.IssueOwnershipRules{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return api.IssueOwnershipRules{}, err
	}
	return api.IssueOwnershipRules{Rules: rules}, nil
}

func (s *PgStore) GetIssueImpactAlertPolicy(ctx context.Context, app string) (api.IssueImpactAlertPolicy, error) {
	row, err := sqlc.New().IssueGetImpactAlertPolicy(ctx, s.pool, issueUUID(app))
	if err != nil {
		return api.IssueImpactAlertPolicy{}, err
	}
	minimum := int64(row)
	return api.IssueImpactAlertPolicy{
		Enabled: minimum > 0, MinimumCustomers: minimum,
		WindowSeconds: int64(api.IssueImpactAlertWindow / time.Second),
	}, nil
}

func (s *PgStore) SetIssueImpactAlertPolicy(ctx context.Context, app, account string, minimum int64, now time.Time) (api.IssueImpactAlertPolicy, error) {
	if minimum < 0 || minimum > api.IssueImpactAlertMaxCustomers {
		return api.IssueImpactAlertPolicy{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueImpactAlertPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueImpactAlertPolicy{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueImpactAlertPolicy{}, ErrNotFound
	}
	if minimum == 0 {
		err = q.IssueDeleteImpactAlertPolicy(ctx, tx, issueUUID(app))
	} else {
		err = q.IssueUpsertImpactAlertPolicy(ctx, tx, sqlc.IssueUpsertImpactAlertPolicyParams{
			AppID: issueUUID(app), MinimumCustomers: int32(minimum), UpdatedAt: issueTime(now),
		})
	}
	if err != nil {
		return api.IssueImpactAlertPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.IssueImpactAlertPolicy{}, err
	}
	return api.IssueImpactAlertPolicy{
		Enabled: minimum > 0, MinimumCustomers: minimum,
		WindowSeconds: int64(api.IssueImpactAlertWindow / time.Second),
	}, nil
}

func (s *PgStore) ListIssues(ctx context.Context, app string, filter IssueListFilter, cur IssueCursor) (api.ListIssuesResponse, error) {
	if err := ValidateIssueListCursor(filter, cur); err != nil {
		return api.ListIssuesResponse{}, err
	}
	sortBy := filter.Sort
	if sortBy == "" {
		sortBy = "recent"
	}
	tags, err := api.ParseIssueTagFilter(filter.Tag)
	if err != nil {
		return api.ListIssuesResponse{}, ErrInvalidArgument
	}
	tagFilter, _ := json.Marshal(tags)
	if sortBy == "impact" || filter.MinCustomers > 0 || filter.Tag != "" {
		windowEnd := time.Now().UTC()
		if cur.ImpactWindowEnd != nil {
			windowEnd = cur.ImpactWindowEnd.UTC()
		}
		rows, err := sqlc.New().IssueListByImpact(ctx, s.pool, sqlc.IssueListByImpactParams{
			TagFilter:    tagFilter,
			MinCustomers: filter.MinCustomers, SortByImpact: sortBy == "impact",
			CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID),
			CursorCustomers: pgtype.Int8{Int64: cur.ImpactCustomers, Valid: cur.ID != "" && sortBy == "impact"},
			PageLimit:       api.IssuePageSize + 1, AppID: issueUUID(app), State: filter.State, Environment: filter.Environment,
			AssigneeAccountID: issueUUID(filter.AssigneeAccountID), AssigneeTeamID: issueUUID(filter.TeamID), Unassigned: filter.Unassigned,
			Since: issueTime(windowEnd.Add(-api.IssueImpactSummaryWindow)), Until: issueTime(windowEnd),
		})
		if err != nil {
			return api.ListIssuesResponse{}, err
		}
		out := api.ListIssuesResponse{Items: make([]api.Issue, 0, min(len(rows), api.IssuePageSize))}
		for i, row := range rows {
			if i == api.IssuePageSize {
				break
			}
			out.Items = append(out.Items, issueImpactListRow(row))
		}
		if len(rows) > api.IssuePageSize {
			last := out.Items[len(out.Items)-1]
			next := IssueCursor{FilterHash: issueListFilterHash(filter), Time: last.LastSeenAt, ID: last.ID, Sort: sortBy, MinCustomers: filter.MinCustomers, ImpactWindowEnd: &windowEnd, Tag: filter.Tag}
			if sortBy == "impact" && last.Impact24h != nil {
				next.ImpactCustomers = last.Impact24h.IdentifiedCustomers
			}
			out.NextCursor = EncodeIssueCursor(next)
		}
		return out, nil
	}
	rows, err := sqlc.New().IssueList(ctx, s.pool, sqlc.IssueListParams{
		AppID: issueUUID(app), State: filter.State, Environment: filter.Environment,
		AssigneeAccountID: issueUUID(filter.AssigneeAccountID), AssigneeTeamID: issueUUID(filter.TeamID), Unassigned: filter.Unassigned,
		CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID), PageLimit: api.IssuePageSize + 1,
	})
	if err != nil {
		return api.ListIssuesResponse{}, err
	}
	out := api.ListIssuesResponse{Items: make([]api.Issue, 0, len(rows))}
	for i, r := range rows {
		if i == api.IssuePageSize {
			break
		}
		out.Items = append(out.Items, issueRow(sqlc.AppIssue(r)))
	}
	if len(rows) > api.IssuePageSize {
		last := out.Items[len(out.Items)-1]
		out.NextCursor = EncodeIssueCursor(IssueCursor{FilterHash: issueListFilterHash(filter), Time: last.LastSeenAt, ID: last.ID})
	}
	if len(out.Items) == 0 {
		return out, nil
	}
	issueIDs := make([]pgtype.UUID, 0, len(out.Items))
	for _, item := range out.Items {
		issueIDs = append(issueIDs, issueUUID(item.ID))
	}
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-api.IssueImpactSummaryWindow)
	impactRows, err := sqlc.New().IssueImpactSummaries(ctx, s.pool, sqlc.IssueImpactSummariesParams{
		IssueIds: issueIDs, Since: issueTime(windowStart), Until: issueTime(windowEnd),
	})
	if err != nil {
		return api.ListIssuesResponse{}, err
	}
	impacts := make(map[string]api.IssueImpactSummary, len(impactRows))
	for _, row := range impactRows {
		impacts[issueID(row.IssueID)] = api.IssueImpactSummary{
			IdentifiedCustomers: row.IdentifiedCustomers,
			ObservedEvents:      row.ObservedEvents,
			UnattributedEvents:  row.UnattributedEvents,
			UnknownHTTPBatches:  row.UnknownHttpBatches,
		}
	}
	for i := range out.Items {
		impact := impacts[out.Items[i].ID]
		out.Items[i].Impact24h = &impact
	}
	return out, nil
}

func (s *PgStore) GetIssueDetail(ctx context.Context, app, id string, since, until time.Time, cur IssueDetailCursors) (api.IssueDetail, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return api.IssueDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := issueCanonicalRow(ctx, q, tx, app, id)
	if err != nil {
		return api.IssueDetail{}, mapErr(err)
	}
	r, err = issueGroupRow(ctx, q, tx, r)
	if err != nil {
		return api.IssueDetail{}, err
	}
	out := api.IssueDetail{Issue: issueRow(r), Events: []api.IssueOccurrence{}, Releases: []api.IssueRelease{}, Activity: []api.IssueActivity{}}
	members, err := q.IssueGroupMembers(ctx, tx, r.ID)
	if err != nil {
		return out, err
	}
	if len(members) > 1 {
		for _, member := range members {
			out.GroupMembers = append(out.GroupMembers, issueRow(member))
		}
	}
	rules, err := q.IssueListGroupSplitRules(ctx, tx, r.ID)
	if err != nil {
		return out, err
	}
	for _, rule := range rules {
		out.SplitRules = append(out.SplitRules, issueSplitRuleRow(rule))
	}
	events, err := q.IssueListEvents(ctx, tx, sqlc.IssueListEventsParams{IssueID: r.ID, Since: issueTime(since), Until: issueTime(until), CursorTime: issueTime(cur.Events.Time), CursorID: issueUUID(cur.Events.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, e := range events {
		if i == api.IssuePageSize {
			break
		}
		var event api.IssueEvent
		if err = json.Unmarshal(e.Payload, &event); err != nil {
			return out, err
		}
		attr := "unattributed"
		if e.VerifiedConsumerID.Valid || e.VerifiedPlatformTenantID.Valid {
			attr = "verified"
		}
		out.Events = append(out.Events, api.IssueOccurrence{ID: issueID(e.ID), OriginalIssueID: issueID(e.ProvenanceIssueID), OccurrenceCount: e.OccurrenceCount, OccurrenceCountExact: e.OccurrenceWeightKnown || e.OccurrenceCount > 1 || event.SourceKind != "http", IssueEvent: event, GroupingExplanation: issues.ExplainGrouping(event, int(e.GroupingVersion), e.Fingerprint), DebugRequestID: issueDebugRequest(ctx, q, tx, issueID(r.AccountID), issueID(r.AppID), issueID(e.DeploymentID), event), DeploymentID: issueID(e.DeploymentID), ReceivedAt: e.ReceivedAt.Time, VerifiedConsumerID: issueID(e.VerifiedConsumerID), VerifiedPlatformTenantID: issueID(e.VerifiedPlatformTenantID), Attribution: attr})
	}
	if len(events) > api.IssuePageSize {
		last := out.Events[len(out.Events)-1]
		out.NextEventCursor = EncodeIssueCursor(IssueCursor{Time: last.OccurredAt, ID: last.ID})
	}
	releases, err := q.IssueListReleases(ctx, tx, sqlc.IssueListReleasesParams{IssueID: r.ID, CursorTime: issueTime(cur.Releases.Time), CursorID: issueUUID(cur.Releases.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, e := range releases {
		if i == api.IssuePageSize {
			last := out.Releases[len(out.Releases)-1]
			out.NextReleaseCursor = EncodeIssueCursor(IssueCursor{Time: last.FirstSeenAt, ID: last.DeploymentID})
			break
		}
		out.Releases = append(out.Releases, api.IssueRelease{DeploymentID: issueID(e.DeploymentID), CommitSHA: e.CommitSha, ImageDigest: e.ImageDigest, EventCount: e.EventCount, FirstSeenAt: e.FirstSeenAt.Time, LastSeenAt: e.LastSeenAt.Time})
	}
	activity, err := q.IssueListActivity(ctx, tx, sqlc.IssueListActivityParams{IssueID: r.ID, CursorTime: issueTime(cur.Activity.Time), CursorID: issueUUID(cur.Activity.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, a := range activity {
		if i == api.IssuePageSize {
			last := out.Activity[len(out.Activity)-1]
			out.NextActivityCursor = EncodeIssueCursor(IssueCursor{Time: last.CreatedAt, ID: last.ID})
			break
		}
		var details map[string]string
		if err = json.Unmarshal(a.Details, &details); err != nil {
			return out, err
		}
		out.Activity = append(out.Activity, api.IssueActivity{ID: issueID(a.ID), OriginalIssueID: issueID(a.IssueID), Action: a.Action, ActorAccountID: issueID(a.ActorAccountID), CreatedAt: a.CreatedAt.Time, Details: details})
	}
	impact, err := q.IssueImpact(ctx, tx, sqlc.IssueImpactParams{IssueID: r.ID, Since: issueTime(since), Until: issueTime(until)})
	if err != nil {
		return out, err
	}
	out.Impact = api.IssueImpact{WindowStart: since, WindowEnd: until, IdentifiedCustomers: impact.IdentifiedCustomers, ObservedEvents: impact.ObservedEvents, UnattributedEvents: impact.UnattributedEvents, UnknownHTTPBatches: impact.UnknownHttpBatches, Coverage: "observed retained events; uninstrumented or rejected failures are not counted"}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ActOnIssue(ctx context.Context, app, id, actor string, in api.IssueActionRequest, now time.Time) (api.Issue, error) {
	// Compare UUID identities rather than accepted alternate string encodings.
	id = issueID(issueUUID(id))
	if in.TargetIssueID != "" {
		in.TargetIssueID = issueID(issueUUID(in.TargetIssueID))
	}
	if in.MemberIssueID != "" {
		in.MemberIssueID = issueID(issueUUID(in.MemberIssueID))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.Issue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Same lock order as ingestion prevents counter/action races and deadlocks.
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return api.Issue{}, mapErr(err)
	}
	out, err := actOnIssueTx(ctx, q, tx, app, id, actor, in, now)
	if err != nil {
		return api.Issue{}, err
	}
	return out, tx.Commit(ctx)
}

func actOnIssueTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, id, actor string, in api.IssueActionRequest, now time.Time) (api.Issue, error) {
	return actOnIssueTxDetails(ctx, q, tx, app, id, actor, in, now, nil)
}

func actOnIssueTxDetails(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, id, actor string, in api.IssueActionRequest, now time.Time, details map[string]string) (api.Issue, error) {
	canonical, err := q.IssueCanonicalID(ctx, tx, sqlc.IssueCanonicalIDParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	row, err := q.IssueGetLocked(ctx, tx, sqlc.IssueGetLockedParams{AppID: issueUUID(app), ID: canonical})
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	if in.Action == "merge" || in.Action == "split" {
		if in.Action == "merge" {
			row, err = issueMergeGroups(ctx, q, tx, row, id, actor, in, now)
		} else {
			row, err = issueSplitGroup(ctx, q, tx, row, actor, in, now)
		}
		if err != nil {
			return api.Issue{}, err
		}
		return issueRow(row), nil
	}
	p := sqlc.IssueUpdateActionParams{ID: row.ID, State: row.State, Assignee: row.AssigneeAccountID, AssigneeTeamID: row.AssigneeTeamID, ResolvedAt: row.ResolvedAt, FixedDeploymentID: row.FixedDeploymentID, FixedDeploymentCreatedAt: row.FixedDeploymentCreatedAt, IgnoredUntil: row.IgnoredUntil}
	if details == nil {
		details = map[string]string{}
	}
	action := ""
	switch in.Action {
	case "assign":
		account, team, err := api.NormalizeIssueAssignment(in.AssigneeAccountID, in.AssigneeTeamID)
		if err != nil {
			return api.Issue{}, ErrInvalidArgument
		}
		in.AssigneeAccountID, in.AssigneeTeamID = account, team
		if in.AssigneeAccountID != "" || in.AssigneeTeamID != "" {
			ok, err := issueAssignmentAllowed(ctx, q, tx, row.AppID, issueUUID(in.AssigneeAccountID), issueUUID(in.AssigneeTeamID))
			if err != nil {
				return api.Issue{}, err
			}
			if !ok {
				return api.Issue{}, ErrNotFound
			}
		}
		if issueID(row.AssigneeAccountID) == in.AssigneeAccountID && issueID(row.AssigneeTeamID) == in.AssigneeTeamID {
			return issueGroupView(ctx, q, tx, row)
		}
		p.Assignee = issueUUID(in.AssigneeAccountID)
		p.AssigneeTeamID = issueUUID(in.AssigneeTeamID)
		details["assignee_account_id"] = in.AssigneeAccountID
		details["assignee_team_id"] = in.AssigneeTeamID
		action = "assigned"
	case "resolve":
		if row.State == "resolved" && issueID(row.FixedDeploymentID) == in.FixedDeploymentID {
			return issueGroupView(ctx, q, tx, row)
		}
		dep, scopeErr := q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{AppID: row.AppID, AccountID: row.AccountID, DeploymentID: issueUUID(in.FixedDeploymentID), Environment: row.Environment})
		if scopeErr != nil {
			return api.Issue{}, mapErr(scopeErr)
		}
		p.State = "resolved"
		p.ResolvedAt = issueTime(now)
		p.FixedDeploymentID = issueUUID(in.FixedDeploymentID)
		p.FixedDeploymentCreatedAt = dep.CreatedAt
		p.IgnoredUntil = pgtype.Timestamptz{}
		details["fixed_deployment_id"] = in.FixedDeploymentID
		action = "resolved"
		if err = q.IssueAddResolution(ctx, tx, sqlc.IssueAddResolutionParams{IssueID: row.ID, DeploymentID: p.FixedDeploymentID, ResolvedAt: p.ResolvedAt, ActorAccountID: issueUUID(actor)}); err != nil {
			return api.Issue{}, err
		}
	case "expire-ignore":
		if row.State != "ignored" || !row.IgnoredUntil.Valid || row.IgnoredUntil.Time.After(now) {
			return issueGroupView(ctx, q, tx, row)
		}
		p.State = "open"
		p.IgnoredUntil = pgtype.Timestamptz{}
		action = "reopened"
	case "reopen":
		if row.State == "open" {
			return issueGroupView(ctx, q, tx, row)
		}
		p.State = "open"
		p.ResolvedAt = pgtype.Timestamptz{}
		p.FixedDeploymentID = pgtype.UUID{}
		p.FixedDeploymentCreatedAt = pgtype.Timestamptz{}
		p.IgnoredUntil = pgtype.Timestamptz{}
		action = "reopened"
	case "ignore":
		if row.State == "ignored" && in.IgnoredUntil != nil && row.IgnoredUntil.Valid && row.IgnoredUntil.Time.Equal(*in.IgnoredUntil) {
			return issueGroupView(ctx, q, tx, row)
		}
		p.State = "ignored"
		p.IgnoredUntil = issueNullableTime(in.IgnoredUntil)
		action = "ignored"
	default:
		return api.Issue{}, ErrInvalidArgument
	}
	row, err = q.IssueUpdateAction(ctx, tx, p)
	if err != nil {
		return api.Issue{}, err
	}
	if err = q.IssueSyncGroupAction(ctx, tx, row.ID); err != nil {
		return api.Issue{}, err
	}
	row, err = issueGroupRow(ctx, q, tx, row)
	if err != nil {
		return api.Issue{}, err
	}
	if err = issueActivity(ctx, q, tx, row, action, actor, details, now); err != nil {
		return api.Issue{}, err
	}
	return issueRow(row), nil
}

// MaintainIssues is a bounded sweep, independent of whether an app receives
// new events. Issue metadata and release totals outlive occurrence retention.
func (s *PgStore) MaintainIssues(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if _, err := q.IssuePurgePlanEvents(ctx, s.pool, sqlc.IssuePurgePlanEventsParams{Plan: string(plan), Before: issueTime(now.AddDate(0, 0, -plan.IssueLimits().RetentionDays)), BatchLimit: api.IssueMaintenanceBatch}); err != nil {
			return err
		}
	}
	if _, err := q.IssuePurgeExpiredTokens(ctx, s.pool, issueTime(now)); err != nil {
		return err
	}
	if err := s.enrichIssueAttribution(ctx, now); err != nil {
		return err
	}
	if err := s.reviewHistoricalIssueAttribution(ctx, now); err != nil {
		return err
	}
	rows, err := q.IssueExpiredIgnores(ctx, s.pool, sqlc.IssueExpiredIgnoresParams{Now: issueTime(now), BatchLimit: api.IssueMaintenanceBatch})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := s.ActOnIssue(ctx, issueID(r.AppID), issueID(r.ID), "", api.IssueActionRequest{Action: "expire-ignore"}, now); err != nil {
			return err
		}
	}
	return nil
}

func issueDebugRequest(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, dep string, e api.IssueEvent) string {
	if e.RequestID == "" && e.TraceID == "" {
		return ""
	}
	rows, err := q.IssueDebugRequest(ctx, db, sqlc.IssueDebugRequestParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(dep), RequestID: e.RequestID, TraceID: e.TraceID, Since: issueTime(e.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(e.OccurredAt.Add(api.IssueMaxClockSkew))})
	if err == nil && len(rows) == 1 {
		return issueID(rows[0])
	}
	return ""
}

// After an initial prompt check, space retries so one app's retained events do
// not exceed the shared maintenance budget. Complete identities use the same
// cadence and get a final grace period after the evidence window.
const issueVerifiedRecheckInterval = 30 * time.Minute

func (s *PgStore) enrichIssueAttribution(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	// Drain several pages without monopolizing maintenance indefinitely.
	deadline := time.Now().Add(api.IssueMaintenanceInterval / 2)
	blockedApps := make(map[pgtype.UUID]bool)
	var excludedApps []pgtype.UUID
	for batch := 0; batch < api.IssueAttributionMaxBatches; batch++ {
		queryCtx, cancel := context.WithDeadline(ctx, deadline)
		rows, err := q.IssueUnattributedEvents(queryCtx, s.pool, sqlc.IssueUnattributedEventsParams{Before: issueTime(now.Add(-api.IssueMaintenanceInterval)), VerifiedBefore: issueTime(now.Add(-issueVerifiedRecheckInterval)), WindowStart: issueTime(now.Add(-api.IssueAttributionRetryWindow)), FullOldest: issueTime(now.Add(-api.IssueAttributionRetryWindow - issueVerifiedRecheckInterval - api.IssueMaintenanceInterval)), BatchLimit: api.IssueAttributionBatch, ExcludedAppIds: excludedApps})
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil
			}
			return err
		}
		for _, row := range rows {
			if time.Now().After(deadline) {
				return nil
			}
			if blockedApps[row.AppID] {
				continue
			}
			rowCtx, rowCancel := context.WithDeadline(ctx, minTime(deadline, time.Now().Add(api.IssueAttributionRowTimeout)))
			err := s.enrichOneIssueAttribution(rowCtx, q, row, now)
			rowCancel()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				blockedApps[row.AppID] = true
				excludedApps = append(excludedApps, row.AppID)
				continue
			}
			if err != nil {
				return err
			}
		}
		if len(rows) < api.IssueAttributionBatch || time.Now().After(deadline) {
			break
		}
	}
	return nil
}

// issueAttributionSettled reports whether no later evidence can attribute the
// occurrence: it names no request, trace or invocation, or its evidence had
// api.IssueAttributionRetryWindow since ingestion to arrive.
func issueAttributionSettled(e api.IssueEvent, receivedAt, lastChecked, now time.Time) bool {
	if e.RequestID == "" && e.TraceID == "" && e.InvocationID == "" {
		return true
	}
	if receivedAt.After(now.Add(-api.IssueAttributionRetryWindow)) {
		return false
	}
	// If a backlog delayed the first attempt past the window, give evidence
	// at least one further maintenance interval to arrive.
	return lastChecked.After(receivedAt.Add(api.IssueMaintenanceInterval))
}

func (s *PgStore) enrichOneIssueAttribution(ctx context.Context, q *sqlc.Queries, row sqlc.IssueUnattributedEventsRow, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := q.IssueLockApp(ctx, tx, row.AppID); err != nil {
		return mapErr(err)
	}
	canonical, err := q.IssueCanonicalID(ctx, tx, sqlc.IssueCanonicalIDParams{AppID: row.AppID, ID: row.IssueID})
	if err != nil {
		return mapErr(err)
	}
	row.IssueID = canonical
	var e api.IssueEvent
	if err := json.Unmarshal(row.Payload, &e); err != nil {
		return err
	}
	in := RecordIssueParams{Credential: IssueCredential{AccountID: issueID(row.AccountID), AppID: issueID(row.AppID), DeploymentID: issueID(row.DeploymentID), Environment: row.Environment}, Event: e}
	consumer, tenant, attributionErr := issueAttribution(ctx, q, tx, in)
	if errors.Is(attributionErr, ErrNotFound) && e.InvocationID != "" {
		// A deleted invocation must not prevent independent request evidence
		// from revoking an earlier identity. It cannot supply a new identity.
		probe := in
		probe.Event.InvocationID = ""
		observedConsumer, observedTenant, probeErr := issueAttribution(ctx, q, tx, probe)
		if errors.Is(probeErr, ErrIssueAttributionConflict) ||
			row.VerifiedConsumerID.Valid && observedConsumer.Valid && row.VerifiedConsumerID != observedConsumer ||
			row.VerifiedPlatformTenantID.Valid && observedTenant.Valid && row.VerifiedPlatformTenantID != observedTenant {
			attributionErr = ErrIssueAttributionConflict
		} else if probeErr != nil {
			return probeErr
		}
	}
	conflicted := errors.Is(attributionErr, ErrIssueAttributionConflict)
	// Deleted invocations or conflicting evidence never acquire a customer.
	if attributionErr != nil && !errors.Is(attributionErr, ErrNotFound) && !errors.Is(attributionErr, ErrIssueAttributionConflict) {
		return attributionErr
	}
	if attributionErr != nil {
		consumer = pgtype.UUID{}
		tenant = pgtype.UUID{}
	}
	// Revoke partial identity when newly available trusted evidence conflicts.
	if row.VerifiedConsumerID.Valid && consumer.Valid && row.VerifiedConsumerID != consumer || row.VerifiedPlatformTenantID.Valid && tenant.Valid && row.VerifiedPlatformTenantID != tenant {
		conflicted = true
	}
	if conflicted {
		consumer, tenant = pgtype.UUID{}, pgtype.UUID{}
	} else {
		if row.VerifiedConsumerID.Valid && tenant.Valid && (!consumer.Valid || row.VerifiedConsumerID != consumer) {
			tenant = pgtype.UUID{}
		}
		if row.VerifiedPlatformTenantID.Valid && consumer.Valid && (!tenant.Valid || row.VerifiedPlatformTenantID != tenant) {
			consumer = pgtype.UUID{}
		}
		if row.VerifiedConsumerID.Valid {
			consumer = row.VerifiedConsumerID
		}
		if row.VerifiedPlatformTenantID.Valid {
			tenant = row.VerifiedPlatformTenantID
		}
	}
	identityAdded := !row.VerifiedConsumerID.Valid && consumer.Valid || !row.VerifiedPlatformTenantID.Valid && tenant.Valid
	var customersBefore int64
	var minimumCustomers int64
	if identityAdded {
		policy, err := q.IssueGetImpactAlertPolicy(ctx, tx, row.AppID)
		if err != nil {
			return err
		}
		minimumCustomers = int64(policy)
		if minimumCustomers > 0 {
			customersBefore, err = q.IssueImpactCustomerCount(ctx, tx, sqlc.IssueImpactCustomerCountParams{
				IssueID: row.IssueID, Since: issueTime(now.Add(-api.IssueImpactAlertWindow)), Until: issueTime(now),
			})
			if err != nil {
				return err
			}
		}
	}
	checkedAt := issueTime(now)
	if issueAttributionSettled(e, row.ReceivedAt.Time, row.AttributionCheckedAt.Time, now) ||
		(row.VerifiedConsumerID.Valid && row.VerifiedPlatformTenantID.Valid && !row.ReceivedAt.Time.After(now.Add(-api.IssueAttributionRetryWindow))) {
		// Checked for the last time: an infinite check time takes the
		// occurrence out of the retry scan for good, instead of locking its app
		// and rewriting the row every maintenance pass until retention purges it.
		checkedAt = pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}
	}
	changed, err := q.IssueEnrichAttribution(ctx, tx, sqlc.IssueEnrichAttributionParams{AppID: row.AppID, DeploymentID: row.DeploymentID, EventID: row.EventID, ConsumerID: consumer, TenantID: tenant, Now: checkedAt, PreviousConsumerID: row.VerifiedConsumerID, PreviousTenantID: row.VerifiedPlatformTenantID, PreviousCheckedAt: row.AttributionCheckedAt})
	if err != nil {
		return err
	}
	if changed > 0 && (row.AttributionVersion < 2 || row.VerifiedConsumerID != consumer || row.VerifiedPlatformTenantID != tenant) {
		if err := q.IssueMarkAttributionReviewed(ctx, tx, row.ID); err != nil {
			return err
		}
	}
	if changed > 0 && minimumCustomers > 0 && identityAdded {
		customersAfter, countErr := q.IssueImpactCustomerCount(ctx, tx, sqlc.IssueImpactCustomerCountParams{
			IssueID: row.IssueID, Since: issueTime(now.Add(-api.IssueImpactAlertWindow)), Until: issueTime(now),
		})
		if countErr != nil {
			return countErr
		}
		if customersAfter >= minimumCustomers && customersBefore < minimumCustomers {
			issue, getErr := q.IssueGetGroup(ctx, tx, sqlc.IssueGetGroupParams{AppID: row.AppID, ID: row.IssueID})
			if getErr != nil {
				return getErr
			}
			if err := recordIssueImpactThresholdTransition(ctx, q, tx, sqlc.AppIssue(issue), minimumCustomers, customersBefore, customersAfter, issueID(row.DeploymentID), now); err != nil {
				return err
			}
		}
	}
	if changed > 0 && identityAdded {
		// Late customer evidence can be the final condition of a compound
		// alert. Reevaluate under the same app lock and transaction as the
		// attribution update; cooldown receipts prevent duplicate delivery.
		issue, err := q.IssueGetGroup(ctx, tx, sqlc.IssueGetGroupParams{AppID: row.AppID, ID: row.IssueID})
		if err != nil {
			return err
		}
		in.Now = now
		if err = recordIssueCompoundAlerts(ctx, q, tx, sqlc.AppIssue(issue), in); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
