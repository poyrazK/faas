package state

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Full clone copies of API consumers and alert rules (ADR-20261104120000
// amendment 2026-11-28); project_environment_clone_consumers.go and
// project_environment_clone_alert_rules.go hold the rules.

// cloneIdentityEnvironmentDB is the environment_id of a project environment
// slug for consumers and alert rules: NULL for production.
func cloneIdentityEnvironmentDB(ctx context.Context, db sqlc.DBTX, projectID, slug string) (pgtype.UUID, error) {
	if IsProductionScope(slug) {
		return pgtype.UUID{}, nil
	}
	id, err := sqlc.New().ReadProjectEnvironmentCloneSourceEnvironmentID(ctx, db, sqlc.ReadProjectEnvironmentCloneSourceEnvironmentIDParams{
		ProjectID: mustPgUUID(projectID), Slug: slug})
	if err != nil {
		return pgtype.UUID{}, mapErr(err)
	}
	return id, nil
}

func captureCloneConsumersDB(ctx context.Context, db sqlc.DBTX, appID string, environment pgtype.UUID) (projectCloneConsumers, error) {
	q := sqlc.New()
	rows, err := q.ReadProjectEnvironmentCloneConsumers(ctx, db, sqlc.ReadProjectEnvironmentCloneConsumersParams{AppID: mustPgUUID(appID), EnvironmentID: environment})
	if err != nil {
		return projectCloneConsumers{}, mapErr(err)
	}
	captured := projectCloneConsumers{AppID: appID, Consumers: make([]projectCloneConsumer, 0, len(rows))}
	if len(rows) == 0 {
		return normalizeCloneConsumers(captured)
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	byID := make(map[string]int, len(rows))
	for _, row := range rows {
		byID[uuidString(row.ID)] = len(captured.Consumers)
		ids = append(ids, row.ID)
		captured.Consumers = append(captured.Consumers, projectCloneConsumer{SourceConsumerID: uuidString(row.ID), ExternalRef: row.ExternalRef, Name: row.Name})
	}
	keys, err := q.ReadProjectEnvironmentCloneConsumerKeys(ctx, db, ids)
	if err != nil {
		return projectCloneConsumers{}, mapErr(err)
	}
	for _, k := range keys {
		c := &captured.Consumers[byID[uuidString(k.ConsumerID)]]
		c.Keys = append(c.Keys, projectCloneConsumerKey{SourceKeyID: uuidString(k.ID), Name: k.Name, Prefix: k.Prefix,
			Scopes: slices.Clone(k.Scopes), ExpiresAt: pgTimePtr(k.ExpiresAt)})
	}
	plans, err := q.ReadProjectEnvironmentCloneConsumerPlans(ctx, db, ids)
	if err != nil {
		return projectCloneConsumers{}, mapErr(err)
	}
	for _, p := range plans {
		c := &captured.Consumers[byID[uuidString(p.ConsumerID)]]
		c.Plans = append(c.Plans, projectCloneConsumerPlanAssignment{PlanID: uuidString(p.PlanID), EffectiveFrom: p.EffectiveFrom.Time})
	}
	return normalizeCloneConsumers(captured)
}

func pgTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time.UTC()
	return &at
}

func pgTimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func captureCloneAlertRulesDB(ctx context.Context, db sqlc.DBTX, appID string, environment pgtype.UUID) (projectCloneAlertRules, error) {
	rules, channels, err := readCloneAlertRulesDB(ctx, db, appID, environment)
	if err != nil {
		return projectCloneAlertRules{}, err
	}
	captured := projectCloneAlertRules{AppID: appID}
	for _, r := range rules {
		if !cloneableAlertRule(r) {
			captured.LeftWithSource++
			continue
		}
		captured.Rules = append(captured.Rules, projectCloneAlertRule{Rule: r, Channels: channels[r.ID]})
	}
	return normalizeCloneAlertRules(captured)
}

func readCloneAlertRulesDB(ctx context.Context, db sqlc.DBTX, appID string, environment pgtype.UUID) ([]AlertRule, map[string][]string, error) {
	q := sqlc.New()
	rows, err := q.ReadProjectEnvironmentCloneAlertRules(ctx, db, sqlc.ReadProjectEnvironmentCloneAlertRulesParams{AppID: mustPgUUID(appID), EnvironmentID: environment})
	if err != nil {
		return nil, nil, mapErr(err)
	}
	rules := make([]AlertRule, 0, len(rows))
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, customerAlertRule(row))
		ids = append(ids, row.ID)
	}
	channels := map[string][]string{}
	if len(ids) == 0 {
		return rules, channels, nil
	}
	bound, err := q.ReadProjectEnvironmentCloneAlertRuleChannels(ctx, db, ids)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	for _, b := range bound {
		channels[uuidString(b.RuleID)] = append(channels[uuidString(b.RuleID)], uuidString(b.ChannelID))
	}
	return rules, channels, nil
}

// requireCloneIdentitiesCapturedDB refuses a source whose environment holds
// active consumers or alert rules on project apps outside the captured
// workloads: the clone would drop them.
func requireCloneIdentitiesCapturedDB(ctx context.Context, db sqlc.DBTX, projectID string, environment pgtype.UUID, appIDs []string) error {
	ids := make([]pgtype.UUID, 0, len(appIDs))
	for _, id := range appIDs {
		ids = append(ids, mustPgUUID(id))
	}
	q := sqlc.New()
	n, err := q.CountProjectEnvironmentCloneUncapturedConsumers(ctx, db, sqlc.CountProjectEnvironmentCloneUncapturedConsumersParams{
		ProjectID: mustPgUUID(projectID), EnvironmentID: environment, AppIds: ids})
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return ErrProjectEnvironmentCloneConsumerCaptureUnavailable
	}
	n, err = q.CountProjectEnvironmentCloneUncapturedAlertRules(ctx, db, sqlc.CountProjectEnvironmentCloneUncapturedAlertRulesParams{
		ProjectID: mustPgUUID(projectID), EnvironmentID: environment, AppIds: ids})
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return ErrProjectEnvironmentCloneAlertRuleCaptureUnavailable
	}
	return nil
}

// checkCloneIdentityQuotaTx counts copied keys and rules against the plan.
// It takes the quota lock of the apps' billing subject, as every key issue
// and rule create does, so the counts hold until the copy commits.
func checkCloneIdentityQuotaTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, limits api.Limits) error {
	keys, rules := cloneConsumerKeyCounts(clone.capturedConsumers), cloneAlertRuleCounts(clone.capturedAlertRules)
	var first string
	for _, appID := range slices.Sorted(maps.Keys(keys)) {
		if keys[appID] > 0 {
			first = appID
			break
		}
	}
	for _, appID := range slices.Sorted(maps.Keys(rules)) {
		if rules[appID] > 0 && (first == "" || appID < first) {
			first = appID
			break
		}
	}
	if first == "" {
		return nil
	}
	subject, _, err := lockQuotaSubjectTx(ctx, tx, first)
	if err != nil {
		return err
	}
	var subjectKeys, subjectRules int
	if err := tx.QueryRow(ctx, `select count(*) from consumer_keys k
		join apps a on a.id = k.app_id
		left join orgs o on o.id = a.org_id
		where k.revoked_at is null and (a.account_id = $1::uuid or a.org_id = $1::uuid)
		  and `+quotaSubjectSQL+` = $1::uuid and a.status <> 'deleted'`, subject).Scan(&subjectKeys); err != nil {
		return fmt.Errorf("state: count consumer keys of billing subject %s: %w", subject, err)
	}
	if err := checkCloneConsumerKeyQuota(keys, func(appID string) (int, error) {
		var n int
		err := tx.QueryRow(ctx, `select count(*) from consumer_keys where app_id = $1::uuid and revoked_at is null`, appID).Scan(&n)
		return n, mapErr(err)
	}, subjectKeys, limits); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `select count(*) from alert_rules r
		  left join apps a on a.id = r.app_id
		  left join orgs o on o.id = a.org_id
		 where (r.app_id is null and r.account_id = $1::uuid)
		    or (a.status <> 'deleted' and `+quotaSubjectSQL+` = $1::uuid)`, subject).Scan(&subjectRules); err != nil {
		return fmt.Errorf("state: count alert_rules for %s: %w", subject, err)
	}
	return checkCloneAlertRuleQuota(rules, func(appID string) (int, error) {
		var n int
		err := tx.QueryRow(ctx, `select count(*) from alert_rules where app_id = $1::uuid`, appID).Scan(&n)
		return n, mapErr(err)
	}, subjectRules, limits)
}

func copyCapturedConsumersTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, target ProjectEnvironment, result *ProjectEnvironmentCloneResult) error {
	q := sqlc.New()
	environment := mustPgUUID(target.ID)
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedConsumers)) {
		for _, c := range clone.capturedConsumers[appID].Consumers {
			id, err := q.InsertProjectEnvironmentCloneConsumer(ctx, tx, sqlc.InsertProjectEnvironmentCloneConsumerParams{
				ExternalRef: c.ExternalRef, Name: c.Name, EnvironmentID: environment, AppID: mustPgUUID(appID)})
			if err != nil {
				return mapErr(err)
			}
			result.ConsumersCopied++
			for _, k := range c.Keys {
				scopes := k.Scopes
				if scopes == nil {
					scopes = []string{}
				}
				n, err := q.InsertProjectEnvironmentCloneConsumerKey(ctx, tx, sqlc.InsertProjectEnvironmentCloneConsumerKeyParams{
					ConsumerID: id, Name: k.Name, Prefix: k.Prefix, Scopes: scopes, ExpiresAt: pgTimestamptzPtr(k.ExpiresAt),
					SourceKeyID: mustPgUUID(k.SourceKeyID)})
				if err != nil {
					return mapErr(err)
				}
				result.ConsumerKeysCopied += int(n)
			}
			for _, p := range c.Plans {
				if err := q.InsertProjectEnvironmentCloneConsumerPlan(ctx, tx, sqlc.InsertProjectEnvironmentCloneConsumerPlanParams{
					PlanID: mustPgUUID(p.PlanID), EffectiveFrom: pgtype.Timestamptz{Time: p.EffectiveFrom, Valid: true}, ConsumerID: id}); err != nil {
					return mapErr(err)
				}
			}
		}
	}
	return nil
}

func copyCapturedAlertRulesTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, target ProjectEnvironment, result *ProjectEnvironmentCloneResult) error {
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedAlertRules)) {
		captured := clone.capturedAlertRules[appID]
		result.AlertRulesLeftWithSource += captured.LeftWithSource
		for _, r := range captured.Rules {
			rule := cloneAlertRuleConfig(r.Rule)
			rule.AccountID, rule.AppID, rule.EnvironmentID, rule.Enabled = clone.AccountID, appID, target.ID, false
			created, err := insertCustomerAlertRule(ctx, tx, rule)
			if err != nil {
				return err
			}
			if len(r.Channels) > 0 {
				if err := setAlertRuleChannelsTx(ctx, tx, mustPgUUID(created.ID), r.Channels); err != nil {
					return err
				}
			}
			result.AlertRulesCopied++
		}
	}
	return nil
}

func verifyCloneIdentityPublicationDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, db, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	environment, err := cloneIdentityEnvironmentDB(ctx, db, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := verifyCloneConsumerRecordDB(ctx, db, record, environment); err != nil {
			return err
		}
		if err := verifyCloneAlertRuleRecordDB(ctx, db, record, environment); err != nil {
			return err
		}
	}
	return nil
}

func verifyCloneConsumerRecordDB(ctx context.Context, db sqlc.DBTX, record projectCloneWorkloadRecord, environment pgtype.UUID) error {
	consumers, err := readCloneConsumerPublicationDB(ctx, db, record.AppID, environment)
	if err != nil {
		return err
	}
	return validateCloneConsumerPublication(record, consumers, func(consumerID, keyID string) (bool, error) {
		key := pgtype.UUID{}
		if keyID != "" {
			key = mustPgUUID(keyID)
		}
		active, err := sqlc.New().ReadProjectEnvironmentCloneConsumerSourceActive(ctx, db, sqlc.ReadProjectEnvironmentCloneConsumerSourceActiveParams{
			KeyID: key, ConsumerID: mustPgUUID(consumerID)})
		return active, mapErr(err)
	})
}

func verifyCloneAlertRuleRecordDB(ctx context.Context, db sqlc.DBTX, record projectCloneWorkloadRecord, environment pgtype.UUID) error {
	rules, channels, err := readCloneAlertRulesDB(ctx, db, record.AppID, environment)
	if err != nil {
		return err
	}
	actual := make([]projectCloneAlertRule, 0, len(rules))
	for _, r := range rules {
		actual = append(actual, projectCloneAlertRule{Rule: r, Channels: channels[r.ID]})
	}
	return validateCloneAlertRulePublication(record, actual)
}

func readCloneConsumerPublicationDB(ctx context.Context, db sqlc.DBTX, appID string, environment pgtype.UUID) ([]cloneConsumerPublication, error) {
	q := sqlc.New()
	rows, err := q.ReadProjectEnvironmentCloneTargetConsumers(ctx, db, sqlc.ReadProjectEnvironmentCloneTargetConsumersParams{AppID: mustPgUUID(appID), EnvironmentID: environment})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]cloneConsumerPublication, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	byID := map[string]int{}
	for _, row := range rows {
		byID[uuidString(row.ID)] = len(out)
		ids = append(ids, row.ID)
		out = append(out, cloneConsumerPublication{Consumer: projectCloneConsumer{ExternalRef: row.ExternalRef, Name: row.Name},
			Active: row.Active, KeyRevoked: map[string]bool{}})
	}
	keys, err := q.ReadProjectEnvironmentCloneTargetConsumerKeys(ctx, db, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	for _, k := range keys {
		p := &out[byID[uuidString(k.ConsumerID)]]
		p.Consumer.Keys = append(p.Consumer.Keys, projectCloneConsumerKey{Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, ExpiresAt: pgTimePtr(k.ExpiresAt)})
		p.KeyRevoked[k.Prefix] = k.Revoked
	}
	plans, err := q.ReadProjectEnvironmentCloneConsumerPlans(ctx, db, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	for _, pl := range plans {
		p := &out[byID[uuidString(pl.ConsumerID)]]
		p.Consumer.Plans = append(p.Consumer.Plans, projectCloneConsumerPlanAssignment{PlanID: uuidString(pl.PlanID), EffectiveFrom: pl.EffectiveFrom.Time})
	}
	return out, nil
}
