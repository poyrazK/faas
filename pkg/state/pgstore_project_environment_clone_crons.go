package state

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func captureCloneCronsDB(ctx context.Context, db sqlc.DBTX, appID, scope string) (projectCloneCrons, error) {
	cronScope := NormalizeCronDeploymentScope(scope)
	crons, _, err := readCloneCronsDB(ctx, db, appID, cronScope)
	if err != nil {
		return projectCloneCrons{}, err
	}
	return normalizeCloneCrons(projectCloneCrons{AppID: appID, SourceScope: cronScope, Crons: crons})
}

// readCloneCronsDB reads one app's crons in a cron scope with each one's
// stored enabled flag.
func readCloneCronsDB(ctx context.Context, db sqlc.DBTX, appID, cronScope string) ([]projectCloneCron, []bool, error) {
	rows, err := sqlc.New().ReadProjectEnvironmentCloneCrons(ctx, db, sqlc.ReadProjectEnvironmentCloneCronsParams{AppID: mustPgUUID(appID), Scope: cronScope})
	if err != nil {
		return nil, nil, mapErr(err)
	}
	crons := make([]projectCloneCron, 0, len(rows))
	stored := make([]bool, 0, len(rows))
	for _, row := range rows {
		c := projectCloneCron{Schedule: row.Schedule, Path: row.Path, Command: row.Command, CommandShell: row.CommandShell,
			CommandTimeoutSeconds: int(row.CommandTimeoutSeconds), CommandMaxOutputBytes: int(row.CommandMaxOutputBytes),
			RetryMax: int(row.RetryMax), RetryBackoffSeconds: int(row.RetryBackoffSeconds), Enabled: row.Enabled,
			Timezone: row.Timezone, SkipIfRunning: row.SkipIfRunning}
		if len(row.SchedulePolicy) > 0 {
			var policy workpolicy.SchedulePolicy
			if err := json.Unmarshal(row.SchedulePolicy, &policy); err != nil {
				return nil, nil, ErrConflict
			}
			c.SchedulePolicy = &policy
		}
		if len(row.FailureRules) > 0 {
			var rules workpolicy.FailureRules
			if err := json.Unmarshal(row.FailureRules, &rules); err != nil {
				return nil, nil, ErrConflict
			}
			c.FailureRules = &rules
		}
		if len(row.Labels) > 0 {
			if err := json.Unmarshal(row.Labels, &c.Labels); err != nil {
				return nil, nil, ErrConflict
			}
		}
		crons = append(crons, c)
		stored = append(stored, row.StoredEnabled)
	}
	return crons, stored, nil
}

// requireCloneCronsCapturedDB refuses a source whose environment holds crons
// on project apps outside the captured workloads: the clone would drop them.
func requireCloneCronsCapturedDB(ctx context.Context, db sqlc.DBTX, projectID, source string, appIDs []string) error {
	ids := make([]pgtype.UUID, 0, len(appIDs))
	for _, id := range appIDs {
		ids = append(ids, mustPgUUID(id))
	}
	n, err := sqlc.New().CountProjectEnvironmentCloneUncapturedCrons(ctx, db, sqlc.CountProjectEnvironmentCloneUncapturedCronsParams{
		ProjectID: mustPgUUID(projectID), Scope: NormalizeCronDeploymentScope(source), AppIds: ids})
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return ErrProjectEnvironmentCloneCronCaptureUnavailable
	}
	return nil
}

// checkCloneCronQuotaTx counts the copy against the plan's cron limits. With
// lock it takes the cron quota lock of the apps' billing subject first, as
// every cron create does, so the count holds until the copy commits.
func checkCloneCronQuotaTx(ctx context.Context, tx pgx.Tx, crons map[string]projectCloneCrons, limits api.Limits, lock bool) error {
	perApp, total := cloneCronCounts(crons)
	if total == 0 {
		return nil
	}
	first := slices.Sorted(maps.Keys(perApp))[0]
	var subject string
	if lock {
		_, locked, err := lockCronQuotaSubjectTx(ctx, tx, first)
		if err != nil {
			return err
		}
		subject = locked
	} else {
		var accountID string
		var orgID pgtype.UUID
		if err := tx.QueryRow(ctx, `select account_id::text, org_id from apps where id = $1`, first).Scan(&accountID, &orgID); err != nil {
			return mapErr(err)
		}
		s, err := cronQuotaSubjectTx(ctx, tx, accountID, orgID)
		if err != nil {
			return err
		}
		subject = s
	}
	subjectCount, err := countQuotaSubjectCronsTx(ctx, tx, subject)
	if err != nil {
		return err
	}
	return checkCloneCronQuota(perApp, func(appID string) (int, error) {
		var n int
		err := tx.QueryRow(ctx, `select count(*) from crons where app_id = $1`, appID).Scan(&n)
		return n, mapErr(err)
	}, total, subjectCount, limits)
}

func copyCapturedCronsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, result *ProjectEnvironmentCloneResult) error {
	q := sqlc.New()
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedCrons)) {
		for _, c := range clone.capturedCrons[appID].Crons {
			labels, err := json.Marshal(c.Labels)
			if err != nil {
				return err
			}
			if c.Labels == nil {
				labels = []byte("{}")
			}
			schedulePolicy, _ := policyJSON(c.SchedulePolicy).([]byte)
			failureRules, _ := policyJSON(c.FailureRules).([]byte)
			if err := q.InsertProjectEnvironmentCloneCron(ctx, tx, sqlc.InsertProjectEnvironmentCloneCronParams{
				AppID: mustPgUUID(appID), Schedule: c.Schedule, Path: c.Path, Command: c.Command, CommandShell: c.CommandShell,
				CommandTimeoutSeconds: int32(c.CommandTimeoutSeconds), CommandMaxOutputBytes: int32(c.CommandMaxOutputBytes), //nolint:gosec // bounded by the crons CHECKs
				RetryMax: int32(c.RetryMax), RetryBackoffSeconds: int32(c.RetryBackoffSeconds), //nolint:gosec // bounded by the crons CHECKs
				ClonePaused: c.Enabled, Timezone: c.Timezone, SkipIfRunning: c.SkipIfRunning,
				SchedulePolicy: schedulePolicy, FailureRules: failureRules, Labels: labels, Scope: clone.TargetSlug,
			}); err != nil {
				return mapErr(err)
			}
			result.CronsCopied++
		}
	}
	return nil
}

func verifyCloneCronPublicationDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, db, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	for _, record := range records {
		actual, stored, err := readCloneCronsDB(ctx, db, record.AppID, NormalizeCronDeploymentScope(op.TargetEnvironment))
		if err != nil {
			return err
		}
		if err := validateCloneCronPublication(record, actual, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgStore) ResumeEnvironmentClonePausedCrons(ctx context.Context, accountID, projectID, environment string) ([]Cron, error) {
	if IsProductionScope(environment) {
		return nil, ErrInvalidArgument
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	project, err := parsePgUUID(projectID)
	if err != nil {
		return nil, err
	}
	id, err := sqlc.New().ReadProjectEnvironmentCloneFlagScope(ctx, s.pool, sqlc.ReadProjectEnvironmentCloneFlagScopeParams{
		AccountID: account, ProjectID: project, Environment: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, mapErr(err)
	}
	rows, err := sqlc.New().ResumeEnvironmentClonePausedCrons(ctx, s.pool, id)
	if err != nil {
		return nil, mapErr(err)
	}
	resumed := make([]Cron, 0, len(rows))
	for _, row := range rows {
		resumed = append(resumed, Cron{ID: uuidString(row.ID), AppID: uuidString(row.AppID), DeploymentScope: environment, Enabled: true})
	}
	slices.SortFunc(resumed, func(a, b Cron) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return resumed, nil
}
