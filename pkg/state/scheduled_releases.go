package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Scheduled release states (ADR-20261021152201).
const (
	ScheduledReleasePending    = "scheduled"
	ScheduledReleaseStarted    = "started"
	ScheduledReleaseBlocked    = "blocked"
	ScheduledReleaseExpired    = "expired"
	ScheduledReleaseCancelled  = "cancelled"
	ScheduledReleaseSuperseded = "superseded"
)

// ErrScheduledReleaseNotParked refuses to start a schedule whose release
// already has traffic or a canary ladder.
var ErrScheduledReleaseNotParked = errors.New("state: scheduled release is not parked")

// ScheduledRelease is a release built ahead of time that starts its rollout
// at ReleaseAt. Canary is the api.CanaryPresetSpec the rollout starts with;
// nil starts it at 100%.
type ScheduledRelease struct {
	ID                string
	AccountID         string
	AppID             string
	DeploymentID      string
	ReleaseAt         time.Time
	Canary            json.RawMessage
	NotifyChannelIDs  []string
	OverrideFreeze    string
	OverrideFreezeIDs []string
	State             string
	OutcomeCode       string
	OutcomeDetail     string
	ScheduledBy       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         *time.Time
	// Revision is the release's deployment revision, read with the schedule.
	Revision int
}

// ScheduledReleaseLadder is the canary a scheduled release starts with. A
// zero ladder starts the release at 100%.
type ScheduledReleaseLadder struct {
	Preset       string
	Stages       json.RawMessage
	TotalSteps   int
	FirstPercent int
}

// ScheduledReleaseStart starts one pending schedule.
type ScheduledReleaseStart struct {
	ScheduleID   string
	DeploymentID string
	Ladder       ScheduledReleaseLadder
	Now          time.Time
}

// ScheduledReleaseStore persists scheduled releases.
type ScheduledReleaseStore interface {
	// ScheduleRelease records a pending schedule. The app's earlier pending
	// schedule is cancelled with replacedCode and returned.
	ScheduleRelease(ctx context.Context, s ScheduledRelease, replacedCode, replacedDetail string, now time.Time) (ScheduledRelease, []ScheduledRelease, error)
	// ScheduledRelease returns one schedule (ErrNotFound otherwise).
	ScheduledRelease(ctx context.Context, id string) (ScheduledRelease, error)
	// LatestScheduledRelease returns a release's newest schedule (ErrNotFound
	// otherwise).
	LatestScheduledRelease(ctx context.Context, deploymentID string) (ScheduledRelease, error)
	// ListScheduledReleases lists an account's schedules, or one app's when
	// appID is set: pending ones first, soonest first, then the newest.
	ListScheduledReleases(ctx context.Context, accountID, appID string, limit int) ([]ScheduledRelease, error)
	// DueScheduledReleases lists pending schedules due at now, oldest first.
	DueScheduledReleases(ctx context.Context, now time.Time, limit int) ([]ScheduledRelease, error)
	// FinishScheduledRelease ends a pending schedule without starting it
	// (ErrNotFound when it is no longer pending).
	FinishScheduledRelease(ctx context.Context, id, state, code, detail string, now time.Time) (ScheduledRelease, error)
	// StartScheduledRelease gives a parked release its first traffic and marks
	// the schedule started, in the transaction of a traffic change: the first
	// canary stage with the ladder stamped, or 100%. It returns ErrNotFound
	// when the schedule is no longer pending, ErrScheduledReleaseNotParked when
	// the release already has traffic or a ladder, and the traffic change's
	// own errors.
	StartScheduledRelease(ctx context.Context, start ScheduledReleaseStart) (Deployment, error)
}

var _ ScheduledReleaseStore = (*MemStore)(nil)

// scheduledStartKey carries a ScheduledReleaseStart into the traffic change
// that performs it.
type scheduledStartKey struct{}

func withScheduledStart(ctx context.Context, start ScheduledReleaseStart) context.Context {
	return context.WithValue(ctx, scheduledStartKey{}, start)
}

func scheduledStartFrom(ctx context.Context, deploymentID string) (ScheduledReleaseStart, bool) {
	start, ok := ctx.Value(scheduledStartKey{}).(ScheduledReleaseStart)
	return start, ok && sameDeploymentID(start.DeploymentID, deploymentID)
}

// startPercent is the traffic a scheduled start gives the release.
func (s ScheduledReleaseStart) startPercent() int {
	if s.Ladder.TotalSteps > 0 {
		return s.Ladder.FirstPercent
	}
	return 100
}

// applyScheduledStart stamps the rollout fields of a started release.
func applyScheduledStart(d *Deployment, start ScheduledReleaseStart) {
	now := start.Now.UTC()
	d.TrafficPercentExplicit = false
	d.RolloutStartedAt = &now
	if start.Ladder.TotalSteps > 0 {
		d.CanaryPreset = start.Ladder.Preset
		d.CanaryStages = append(json.RawMessage(nil), start.Ladder.Stages...)
		d.CanaryTotalSteps = start.Ladder.TotalSteps
		d.CanaryStep = 0
		d.CanaryStepStartedAt = &now
		d.RolloutState = "rolling_out"
		d.RolloutCompletedAt = nil
		return
	}
	d.RolloutState = "complete"
	d.RolloutCompletedAt = &now
}

func (m *MemStore) StartScheduledRelease(ctx context.Context, start ScheduledReleaseStart) (Deployment, error) {
	return m.updateDeploymentTraffic(withScheduledStart(ctx, start), start.DeploymentID, start.startPercent(), nil, nil)
}

// checkScheduledStartLocked refuses a start whose schedule is no longer
// pending or whose release is not parked. The caller holds m.mu.
func (m *MemStore) checkScheduledStartLocked(start ScheduledReleaseStart, d Deployment) error {
	s, ok := m.scheduledReleases[start.ScheduleID]
	if !ok || s.State != ScheduledReleasePending || !sameDeploymentID(s.DeploymentID, d.ID) {
		return ErrNotFound
	}
	if d.TrafficPercent != 0 || d.CanaryTotalSteps != 0 {
		return ErrScheduledReleaseNotParked
	}
	return nil
}

// finishScheduledStartLocked marks the schedule started. The caller holds
// m.mu and has checked it with checkScheduledStartLocked.
func (m *MemStore) finishScheduledStartLocked(start ScheduledReleaseStart) {
	s := m.scheduledReleases[start.ScheduleID]
	now := start.Now.UTC()
	s.State, s.StartedAt, s.UpdatedAt = ScheduledReleaseStarted, &now, now
	m.scheduledReleases[start.ScheduleID] = s
}

func (m *MemStore) ScheduleRelease(_ context.Context, s ScheduledRelease, replacedCode, replacedDetail string, now time.Time) (ScheduledRelease, []ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[s.DeploymentID]; !ok {
		return ScheduledRelease{}, nil, ErrNotFound
	}
	now = now.UTC()
	if m.scheduledReleases == nil {
		m.scheduledReleases = map[string]ScheduledRelease{}
	}
	var replaced []ScheduledRelease
	for id, existing := range m.scheduledReleases {
		if existing.AppID == s.AppID && existing.State == ScheduledReleasePending {
			existing.State, existing.OutcomeCode, existing.OutcomeDetail, existing.UpdatedAt = ScheduledReleaseCancelled, replacedCode, replacedDetail, now
			m.scheduledReleases[id] = existing
			replaced = append(replaced, m.withRevisionLocked(existing))
		}
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.ReleaseAt = s.ReleaseAt.UTC()
	s.State, s.OutcomeCode, s.OutcomeDetail, s.StartedAt = ScheduledReleasePending, "", "", nil
	s.CreatedAt, s.UpdatedAt = now, now
	s.Canary = append(json.RawMessage(nil), s.Canary...)
	s.NotifyChannelIDs = append([]string{}, s.NotifyChannelIDs...)
	s.OverrideFreezeIDs = append([]string{}, s.OverrideFreezeIDs...)
	m.scheduledReleases[s.ID] = s
	return m.withRevisionLocked(s), replaced, nil
}

func (m *MemStore) withRevisionLocked(s ScheduledRelease) ScheduledRelease {
	s.Revision = m.deployments[s.DeploymentID].Revision
	return s
}

func (m *MemStore) ScheduledRelease(_ context.Context, id string) (ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scheduledReleases[id]
	if !ok {
		return ScheduledRelease{}, ErrNotFound
	}
	return m.withRevisionLocked(s), nil
}

func (m *MemStore) LatestScheduledRelease(_ context.Context, deploymentID string) (ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *ScheduledRelease
	for _, s := range m.scheduledReleases {
		if !sameDeploymentID(s.DeploymentID, deploymentID) {
			continue
		}
		if latest == nil || s.CreatedAt.After(latest.CreatedAt) || (s.CreatedAt.Equal(latest.CreatedAt) && s.ID > latest.ID) {
			s := s
			latest = &s
		}
	}
	if latest == nil {
		return ScheduledRelease{}, ErrNotFound
	}
	return m.withRevisionLocked(*latest), nil
}

func (m *MemStore) ListScheduledReleases(_ context.Context, accountID, appID string, limit int) ([]ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ScheduledRelease
	for _, s := range m.scheduledReleases {
		if s.AccountID == accountID && (appID == "" || s.AppID == appID) {
			out = append(out, m.withRevisionLocked(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i].State == ScheduledReleasePending, out[j].State == ScheduledReleasePending
		switch {
		case pi != pj:
			return pi
		case pi && !out[i].ReleaseAt.Equal(out[j].ReleaseAt):
			return out[i].ReleaseAt.Before(out[j].ReleaseAt)
		case !out[i].UpdatedAt.Equal(out[j].UpdatedAt):
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) DueScheduledReleases(_ context.Context, now time.Time, limit int) ([]ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ScheduledRelease
	for _, s := range m.scheduledReleases {
		if s.State == ScheduledReleasePending && !s.ReleaseAt.After(now) {
			out = append(out, m.withRevisionLocked(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ReleaseAt.Equal(out[j].ReleaseAt) {
			return out[i].ReleaseAt.Before(out[j].ReleaseAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) FinishScheduledRelease(_ context.Context, id, state, code, detail string, now time.Time) (ScheduledRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scheduledReleases[id]
	if !ok || s.State != ScheduledReleasePending {
		return ScheduledRelease{}, ErrNotFound
	}
	s.State, s.OutcomeCode, s.OutcomeDetail, s.UpdatedAt = state, code, detail, now.UTC()
	m.scheduledReleases[id] = s
	return m.withRevisionLocked(s), nil
}
