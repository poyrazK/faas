package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Database switch phases (ADR-20261023181931). A switch moves through them in
// order; aborting runs the compensation that ends in failed or cancelled.
const (
	DatabaseSwitchRestoring = "restoring"
	DatabaseSwitchStaging   = "staging"
	DatabaseSwitchVerifying = "verifying"
	DatabaseSwitchFencing   = "fencing"
	DatabaseSwitchSettling  = "settling"
	DatabaseSwitchDraining  = "draining"
	DatabaseSwitchSwitching = "switching"
	DatabaseSwitchResuming  = "resuming"
	DatabaseSwitchAborting  = "aborting"
	DatabaseSwitchCompleted = "completed"
	DatabaseSwitchFailed    = "failed"
	DatabaseSwitchCancelled = "cancelled"
)

// DatabaseSwitchTerminal reports whether a phase ends the switch.
func DatabaseSwitchTerminal(phase string) bool {
	return phase == DatabaseSwitchCompleted || phase == DatabaseSwitchFailed || phase == DatabaseSwitchCancelled
}

// DatabaseSwitchCancellable reports whether a person may still cancel a
// switch in phase: until it starts switching the release and the bindings.
func DatabaseSwitchCancellable(phase string) bool {
	switch phase {
	case DatabaseSwitchRestoring, DatabaseSwitchStaging, DatabaseSwitchVerifying,
		DatabaseSwitchFencing, DatabaseSwitchSettling, DatabaseSwitchDraining:
		return true
	}
	return false
}

// DatabaseSwitchDrain counts the drain plan's members by outcome.
type DatabaseSwitchDrain struct {
	Retired, Parked, Pending, Blocked int
}

// DatabaseSwitch is a rollback that restores the app's managed PostgreSQL
// database with the code (ADR-20261023181931). UndoneDeploymentIDs are the
// deployments whose release commands the restore undoes.
type DatabaseSwitch struct {
	ID                  string
	AccountID           string
	AppID               string
	Scope               string
	FromDeploymentID    string
	TargetDeploymentID  string
	UndoneDeploymentIDs []string
	SourceDatabaseID    string
	TargetDatabaseID    string
	TargetName          string
	RestorePoint        time.Time
	CutoverID           string
	Phase               string
	FailureCode         string
	FailureDetail       string
	RequestedBy         string
	CancelRequestedAt   *time.Time
	FencedAt            *time.Time
	DrainRequestedAt    *time.Time
	ReleaseSwitchedAt   *time.Time
	ActivatedAt         *time.Time
	FinishedAt          *time.Time
	PhaseStartedAt      time.Time
	Drain               DatabaseSwitchDrain
	AttemptCount        int
	LastErrorCode       string
	LeaseToken          string
	LeaseUntil          *time.Time
	RetryAt             time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DatabaseSwitchStore persists database switches. apid writes them; schedd
// reads the ones waiting for a drain plan.
type DatabaseSwitchStore interface {
	// CreateDatabaseSwitch records a new switch in phase restoring. It
	// returns ErrConflict when the app already has an active switch.
	CreateDatabaseSwitch(ctx context.Context, sw DatabaseSwitch) (DatabaseSwitch, error)
	// DatabaseSwitch returns one of an account's switches (ErrNotFound
	// otherwise).
	DatabaseSwitch(ctx context.Context, accountID, id string) (DatabaseSwitch, error)
	// ListDatabaseSwitches lists an app's switches, newest first.
	ListDatabaseSwitches(ctx context.Context, accountID, appID string, limit int) ([]DatabaseSwitch, error)
	// ClaimDueDatabaseSwitches leases up to limit active switches whose
	// retry time has come and whose lease is free or expired.
	ClaimDueDatabaseSwitches(ctx context.Context, token string, now, until time.Time, limit int) ([]DatabaseSwitch, error)
	// SaveDatabaseSwitch writes a leased switch's progress and releases the
	// lease. It returns ErrConflict when the lease was lost. A cancellation
	// request recorded meanwhile is kept.
	SaveDatabaseSwitch(ctx context.Context, sw DatabaseSwitch, now time.Time) (DatabaseSwitch, error)
	// RequestDatabaseSwitchCancel records a cancellation request. It
	// returns ErrConflict once the switch is past cancelling.
	RequestDatabaseSwitchCancel(ctx context.Context, accountID, appID, id string, now time.Time) (DatabaseSwitch, error)
	// DrainingDatabaseSwitches lists switches that asked schedd for a drain
	// plan, oldest first.
	DrainingDatabaseSwitches(ctx context.Context, limit int) ([]DatabaseSwitch, error)
	// ReleasesUndoneByDatabaseSwitches lists the deployments whose release
	// commands a completed switch undid in an app scope.
	ReleasesUndoneByDatabaseSwitches(ctx context.Context, appID, scope string) ([]string, error)
}

var _ DatabaseSwitchStore = (*MemStore)(nil)

func validNewDatabaseSwitch(sw DatabaseSwitch) bool {
	for _, id := range []string{sw.AccountID, sw.AppID, sw.TargetDeploymentID, sw.SourceDatabaseID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if sw.FromDeploymentID != "" {
		if _, err := uuid.Parse(sw.FromDeploymentID); err != nil {
			return false
		}
	}
	for _, id := range sw.UndoneDeploymentIDs {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return len(sw.UndoneDeploymentIDs) > 0 && sw.TargetName != "" && len(sw.TargetName) <= 63 &&
		!sw.RestorePoint.IsZero() && normalizedDeploymentScope(sw.Scope) != ""
}

func (m *MemStore) CreateDatabaseSwitch(_ context.Context, sw DatabaseSwitch) (DatabaseSwitch, error) {
	if !validNewDatabaseSwitch(sw) {
		return DatabaseSwitch{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[sw.AppID]; !ok {
		return DatabaseSwitch{}, ErrNotFound
	}
	for _, existing := range m.databaseSwitches {
		if existing.AppID == sw.AppID && !DatabaseSwitchTerminal(existing.Phase) {
			return DatabaseSwitch{}, ErrConflict
		}
	}
	if m.databaseSwitches == nil {
		m.databaseSwitches = map[string]DatabaseSwitch{}
	}
	if sw.ID == "" {
		sw.ID = uuid.NewString()
	}
	now := sw.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sw.Scope = normalizedDeploymentScope(sw.Scope)
	sw.UndoneDeploymentIDs = append([]string{}, sw.UndoneDeploymentIDs...)
	sw.RestorePoint = sw.RestorePoint.UTC()
	sw.Phase, sw.TargetDatabaseID, sw.CutoverID = DatabaseSwitchRestoring, "", ""
	sw.FailureCode, sw.FailureDetail, sw.LastErrorCode = "", "", ""
	sw.CancelRequestedAt, sw.FencedAt, sw.DrainRequestedAt = nil, nil, nil
	sw.ReleaseSwitchedAt, sw.ActivatedAt, sw.FinishedAt = nil, nil, nil
	sw.Drain, sw.AttemptCount, sw.LeaseToken, sw.LeaseUntil = DatabaseSwitchDrain{}, 0, "", nil
	sw.PhaseStartedAt, sw.RetryAt, sw.CreatedAt, sw.UpdatedAt = now, now, now, now
	m.databaseSwitches[sw.ID] = sw
	return copyDatabaseSwitch(sw), nil
}

func (m *MemStore) DatabaseSwitch(_ context.Context, accountID, id string) (DatabaseSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sw, ok := m.databaseSwitches[id]
	if !ok || sw.AccountID != accountID {
		return DatabaseSwitch{}, ErrNotFound
	}
	return copyDatabaseSwitch(sw), nil
}

func (m *MemStore) ListDatabaseSwitches(_ context.Context, accountID, appID string, limit int) ([]DatabaseSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DatabaseSwitch
	for _, sw := range m.databaseSwitches {
		if sw.AccountID == accountID && sw.AppID == appID {
			out = append(out, copyDatabaseSwitch(sw))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ClaimDueDatabaseSwitches(_ context.Context, token string, now, until time.Time, limit int) ([]DatabaseSwitch, error) {
	if token == "" || !until.After(now) || limit <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []DatabaseSwitch
	for _, sw := range m.databaseSwitches {
		if DatabaseSwitchTerminal(sw.Phase) || sw.RetryAt.After(now) || (sw.LeaseUntil != nil && sw.LeaseUntil.After(now)) {
			continue
		}
		due = append(due, sw)
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].RetryAt.Equal(due[j].RetryAt) {
			return due[i].RetryAt.Before(due[j].RetryAt)
		}
		return due[i].ID < due[j].ID
	})
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]DatabaseSwitch, 0, len(due))
	for _, sw := range due {
		leaseUntil := until.UTC()
		sw.LeaseToken, sw.LeaseUntil, sw.UpdatedAt = token, &leaseUntil, now.UTC()
		m.databaseSwitches[sw.ID] = sw
		out = append(out, copyDatabaseSwitch(sw))
	}
	return out, nil
}

func (m *MemStore) SaveDatabaseSwitch(_ context.Context, sw DatabaseSwitch, now time.Time) (DatabaseSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.databaseSwitches[sw.ID]
	if !ok {
		return DatabaseSwitch{}, ErrNotFound
	}
	if sw.LeaseToken == "" || current.LeaseToken != sw.LeaseToken || current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
		return DatabaseSwitch{}, ErrConflict
	}
	if err := validDatabaseSwitchProgress(sw); err != nil {
		return DatabaseSwitch{}, err
	}
	saved := current
	applyDatabaseSwitchProgress(&saved, sw)
	saved.LeaseToken, saved.LeaseUntil, saved.UpdatedAt = "", nil, now.UTC()
	m.databaseSwitches[sw.ID] = saved
	return copyDatabaseSwitch(saved), nil
}

func (m *MemStore) RequestDatabaseSwitchCancel(_ context.Context, accountID, appID, id string, now time.Time) (DatabaseSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sw, ok := m.databaseSwitches[id]
	if !ok || sw.AccountID != accountID || sw.AppID != appID {
		return DatabaseSwitch{}, ErrNotFound
	}
	if !DatabaseSwitchCancellable(sw.Phase) {
		return copyDatabaseSwitch(sw), ErrConflict
	}
	if sw.CancelRequestedAt == nil {
		at := now.UTC()
		sw.CancelRequestedAt = &at
	}
	sw.RetryAt, sw.UpdatedAt = now.UTC(), now.UTC()
	m.databaseSwitches[id] = sw
	return copyDatabaseSwitch(sw), nil
}

func (m *MemStore) DrainingDatabaseSwitches(_ context.Context, limit int) ([]DatabaseSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DatabaseSwitch
	for _, sw := range m.databaseSwitches {
		if sw.Phase == DatabaseSwitchDraining && sw.DrainRequestedAt != nil && sw.CutoverID != "" {
			out = append(out, copyDatabaseSwitch(sw))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DrainRequestedAt.Before(*out[j].DrainRequestedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ReleasesUndoneByDatabaseSwitches(_ context.Context, appID, scope string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scope = normalizedDeploymentScope(scope)
	var out []string
	for _, sw := range m.databaseSwitches {
		if sw.AppID != appID || sw.Scope != scope || sw.Phase != DatabaseSwitchCompleted {
			continue
		}
		for _, id := range sw.UndoneDeploymentIDs {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// validDatabaseSwitchProgress checks the fields a save may change.
func validDatabaseSwitchProgress(sw DatabaseSwitch) error {
	switch sw.Phase {
	case DatabaseSwitchRestoring, DatabaseSwitchStaging, DatabaseSwitchVerifying, DatabaseSwitchFencing,
		DatabaseSwitchSettling, DatabaseSwitchDraining, DatabaseSwitchSwitching, DatabaseSwitchResuming,
		DatabaseSwitchAborting, DatabaseSwitchCompleted, DatabaseSwitchFailed, DatabaseSwitchCancelled:
	default:
		return ErrInvalidArgument
	}
	if DatabaseSwitchTerminal(sw.Phase) != (sw.FinishedAt != nil) {
		return ErrInvalidArgument
	}
	if (sw.Phase == DatabaseSwitchFailed || sw.Phase == DatabaseSwitchAborting) && sw.FailureCode == "" {
		return ErrInvalidArgument
	}
	if sw.ActivatedAt != nil && sw.Phase != DatabaseSwitchSwitching && sw.Phase != DatabaseSwitchResuming && sw.Phase != DatabaseSwitchCompleted {
		return ErrInvalidArgument
	}
	if sw.Phase == DatabaseSwitchCompleted && (sw.ActivatedAt == nil || sw.ReleaseSwitchedAt == nil || sw.TargetDatabaseID == "" || sw.CutoverID == "") {
		return ErrInvalidArgument
	}
	return nil
}

func applyDatabaseSwitchProgress(dst *DatabaseSwitch, src DatabaseSwitch) {
	dst.TargetDatabaseID, dst.CutoverID, dst.Phase = src.TargetDatabaseID, src.CutoverID, src.Phase
	dst.FailureCode, dst.FailureDetail, dst.LastErrorCode = src.FailureCode, src.FailureDetail, src.LastErrorCode
	dst.FencedAt, dst.DrainRequestedAt = utcTimePtr(src.FencedAt), utcTimePtr(src.DrainRequestedAt)
	dst.ReleaseSwitchedAt, dst.ActivatedAt = utcTimePtr(src.ReleaseSwitchedAt), utcTimePtr(src.ActivatedAt)
	dst.FinishedAt, dst.PhaseStartedAt = utcTimePtr(src.FinishedAt), src.PhaseStartedAt.UTC()
	dst.Drain, dst.AttemptCount, dst.RetryAt = src.Drain, src.AttemptCount, src.RetryAt.UTC()
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func copyDatabaseSwitch(sw DatabaseSwitch) DatabaseSwitch {
	sw.UndoneDeploymentIDs = append([]string{}, sw.UndoneDeploymentIDs...)
	for _, p := range []**time.Time{&sw.CancelRequestedAt, &sw.FencedAt, &sw.DrainRequestedAt, &sw.ReleaseSwitchedAt,
		&sw.ActivatedAt, &sw.FinishedAt, &sw.LeaseUntil} {
		*p = utcTimePtr(*p)
	}
	return sw
}
