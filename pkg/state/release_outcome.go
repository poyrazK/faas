package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReleaseOutcomeCheck records that a person checked the database after a
// release command whose outcome is unknown (ADR-1448 amendment 1).
type ReleaseOutcomeCheck struct {
	TaskID       string
	AccountID    string
	AppID        string
	DeploymentID string
	CheckedBy    string
	Note         string
	CheckedAt    time.Time
}

// ReleaseOutcomeStore is the optional capability behind the uncertain
// release-command rule (ADR-1448 amendment 1). A release command that started
// but ended without an exit code (its worker lease expired, it timed out or
// it was cancelled while running) may have changed the database partly, so
// the next release command of the app scope does not start until a person
// records a check, or a later release command in the scope succeeded.
type ReleaseOutcomeStore interface {
	// UncertainReleaseTask returns the newest such release command of the
	// app scope, other than excludeDeploymentID's, or ErrNotFound.
	UncertainReleaseTask(ctx context.Context, accountID, appID, scope, excludeDeploymentID string) (AppTask, error)
	// RecordReleaseOutcomeCheck records a check of an uncertain release
	// command. ErrConflict when the command's outcome is known or it was
	// checked already.
	RecordReleaseOutcomeCheck(ctx context.Context, check ReleaseOutcomeCheck) (ReleaseOutcomeCheck, error)
}

var (
	_ ReleaseOutcomeStore = (*PgStore)(nil)
	_ ReleaseOutcomeStore = (*MemStore)(nil)
)

// ReleaseOutcomeUnknown reports whether a release command started but ended
// without an exit code.
func ReleaseOutcomeUnknown(t AppTask) bool {
	if t.Kind != AppTaskKindRelease || t.StartedAt == nil || t.ExitCode != nil {
		return false
	}
	return t.Status == AppTaskFailed || t.Status == AppTaskTimedOut || t.Status == AppTaskCancelled
}

func (s *PgStore) UncertainReleaseTask(ctx context.Context, accountID, appID, scope, excludeDeploymentID string) (AppTask, error) {
	if excludeDeploymentID == "" {
		excludeDeploymentID = "00000000-0000-0000-0000-000000000000"
	}
	row, err := sqlc.New().UncertainReleaseTask(ctx, s.pool, sqlc.UncertainReleaseTaskParams{
		AccountID: accountID, AppID: appID, Scope: normalizedDeploymentScope(scope), ExcludeDeploymentID: excludeDeploymentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrNotFound
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return appTaskFromSQLC(row)
}

func (s *PgStore) RecordReleaseOutcomeCheck(ctx context.Context, check ReleaseOutcomeCheck) (ReleaseOutcomeCheck, error) {
	if err := validReleaseOutcomeCheck(&check); err != nil {
		return ReleaseOutcomeCheck{}, err
	}
	row, err := sqlc.New().InsertReleaseOutcomeCheck(ctx, s.pool, sqlc.InsertReleaseOutcomeCheckParams{
		TaskID: check.TaskID, AccountID: check.AccountID, CheckedBy: check.CheckedBy, Note: check.Note,
		CheckedAt: pgtype.Timestamptz{Time: check.CheckedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseOutcomeCheck{}, ErrConflict
	}
	if err != nil {
		return ReleaseOutcomeCheck{}, mapErr(err)
	}
	return ReleaseOutcomeCheck{
		TaskID: row.TaskID, AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID,
		CheckedBy: row.CheckedBy, Note: row.Note, CheckedAt: row.CheckedAt.Time.UTC(),
	}, nil
}

func validReleaseOutcomeCheck(check *ReleaseOutcomeCheck) error {
	check.Note = strings.TrimSpace(check.Note)
	if check.TaskID == "" || check.AccountID == "" || check.CheckedBy == "" || len(check.CheckedBy) > 200 ||
		check.Note == "" || len(check.Note) > 1000 {
		return ErrAppTaskInvalid
	}
	if check.CheckedAt.IsZero() {
		check.CheckedAt = time.Now()
	}
	check.CheckedAt = check.CheckedAt.UTC()
	return nil
}

func (m *MemStore) UncertainReleaseTask(_ context.Context, accountID, appID, scope, excludeDeploymentID string) (AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest *AppTask
	for _, t := range m.appTasks {
		if t.AccountID != accountID || t.AppID != appID || t.DeploymentID == excludeDeploymentID ||
			normalizedDeploymentScope(t.DeploymentScope) != normalizedDeploymentScope(scope) || !m.releaseOutcomeUncertainLocked(t) {
			continue
		}
		if newest == nil || t.StartedAt.After(*newest.StartedAt) || t.StartedAt.Equal(*newest.StartedAt) && t.ID < newest.ID {
			c := t
			newest = &c
		}
	}
	if newest == nil {
		return AppTask{}, ErrNotFound
	}
	return cloneAppTask(*newest), nil
}

func (m *MemStore) RecordReleaseOutcomeCheck(_ context.Context, check ReleaseOutcomeCheck) (ReleaseOutcomeCheck, error) {
	if err := validReleaseOutcomeCheck(&check); err != nil {
		return ReleaseOutcomeCheck{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.appTasks[check.TaskID]
	if !ok || t.AccountID != check.AccountID || !ReleaseOutcomeUnknown(t) {
		return ReleaseOutcomeCheck{}, ErrConflict
	}
	if _, done := m.releaseOutcomeChecks[t.ID]; done {
		return ReleaseOutcomeCheck{}, ErrConflict
	}
	if m.releaseOutcomeChecks == nil {
		m.releaseOutcomeChecks = map[string]ReleaseOutcomeCheck{}
	}
	check.AppID, check.DeploymentID = t.AppID, t.DeploymentID
	m.releaseOutcomeChecks[t.ID] = check
	return check, nil
}

// releaseOutcomeUncertainLocked mirrors UncertainReleaseTask's predicate for
// one task: unknown outcome, unchecked, and no later success in its scope.
func (m *MemStore) releaseOutcomeUncertainLocked(prior AppTask) bool {
	if !ReleaseOutcomeUnknown(prior) {
		return false
	}
	if _, checked := m.releaseOutcomeChecks[prior.ID]; checked {
		return false
	}
	// A promotion migration recovery that resolved the task is its check.
	if m.promotionMigrationTaskResolvedLocked(prior.ID) {
		return false
	}
	scope := normalizedDeploymentScope(prior.DeploymentScope)
	for _, later := range m.appTasks {
		if later.AccountID == prior.AccountID && later.AppID == prior.AppID && later.Kind == AppTaskKindRelease &&
			later.Status == AppTaskSucceeded && later.StartedAt != nil && later.StartedAt.After(*prior.StartedAt) &&
			normalizedDeploymentScope(later.DeploymentScope) == scope {
			return false
		}
	}
	return true
}
