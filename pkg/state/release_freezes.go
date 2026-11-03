package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Release freeze kinds and sources (release_freezes, ADR-20261019071733).
const (
	ReleaseFreezeWindow        = "window"
	ReleaseFreezeWeekly        = "weekly"
	ReleaseFreezeAfterRollback = "after_rollback"

	ReleaseFreezeSourceManual            = "manual"
	ReleaseFreezeSourceAutomaticRollback = "automatic_rollback"
)

// ErrReleaseFreezeLimit means the owner already holds the maximum number of
// unlifted freezes.
var ErrReleaseFreezeLimit = errors.New("state: release freeze limit reached")

// ReleaseFreezeOwner owns freezes: an account, whose freezes cover its
// personal apps, or a company organization, whose freezes cover the
// organization's apps (ADR-20261019071733 amendment 4). Exactly one is set.
type ReleaseFreezeOwner struct {
	AccountID string
	OrgID     string
}

// Valid reports whether exactly one owner is set.
func (o ReleaseFreezeOwner) Valid() bool {
	return (o.AccountID == "") != (o.OrgID == "")
}

// ReleaseFreeze is one freeze on production releases. It belongs to an
// account (AccountID) or to a company organization (OrgID). AppID empty
// covers all of the owner's apps.
type ReleaseFreeze struct {
	ID        string
	AccountID string
	OrgID     string
	AppID     string
	Kind      string
	// StartsAt and EndsAt bound a window; EndsAt nil means until lifted.
	StartsAt *time.Time
	EndsAt   *time.Time
	// WeeklyDays is a bitmask of weekdays (bit 0 = Sunday); the weekly
	// window opens at WeeklyStartMinute in TimeZone and lasts
	// WeeklyDurationMinutes.
	WeeklyDays            int
	WeeklyStartMinute     int
	WeeklyDurationMinutes int
	TimeZone              string
	// AfterRollbackMinutes is how long an after_rollback policy freezes an
	// app Gregale rolled back automatically.
	AfterRollbackMinutes int
	Reason               string
	Source               string
	SourceDeploymentID   string
	CreatedBy            string
	CreatedAt            time.Time
	LiftedAt             *time.Time
	LiftedBy             string
}

// Owner returns the freeze's owner.
func (f ReleaseFreeze) Owner() ReleaseFreezeOwner {
	return ReleaseFreezeOwner{AccountID: f.AccountID, OrgID: f.OrgID}
}

// ReleaseFreezeStore persists freezes (ADR-20261019071733).
type ReleaseFreezeStore interface {
	// CreateReleaseFreeze stores a manual freeze unless its owner already
	// holds maxUnlifted unlifted freezes that have not ended
	// (ErrReleaseFreezeLimit). f names exactly one owner
	// (ErrInvalidArgument otherwise).
	CreateReleaseFreeze(ctx context.Context, f ReleaseFreeze, maxUnlifted int, now time.Time) (ReleaseFreeze, error)
	// CreateRollbackReleaseFreeze stores the automatic window freeze for one
	// rolled-back deployment, once. It reports whether it created the row.
	CreateRollbackReleaseFreeze(ctx context.Context, f ReleaseFreeze) (bool, error)
	// ListReleaseFreezes returns the owner's unlifted freezes that have not
	// ended, oldest first.
	ListReleaseFreezes(ctx context.Context, owner ReleaseFreezeOwner, now time.Time) ([]ReleaseFreeze, error)
	// LiftReleaseFreeze lifts one of the owner's unlifted freezes
	// (ErrNotFound otherwise).
	LiftReleaseFreeze(ctx context.Context, owner ReleaseFreezeOwner, id, liftedBy string, now time.Time) (ReleaseFreeze, error)
}

var _ ReleaseFreezeStore = (*MemStore)(nil)

// releaseFreezeEnded reports whether a window has ended; other kinds never end.
func releaseFreezeEnded(f ReleaseFreeze, now time.Time) bool {
	return f.Kind == ReleaseFreezeWindow && f.EndsAt != nil && !f.EndsAt.After(now)
}

func (m *MemStore) CreateReleaseFreeze(_ context.Context, f ReleaseFreeze, maxUnlifted int, now time.Time) (ReleaseFreeze, error) {
	if !f.Owner().Valid() {
		return ReleaseFreeze{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	open := 0
	for _, existing := range m.releaseFreezes {
		if existing.Owner() == f.Owner() && existing.LiftedAt == nil && !releaseFreezeEnded(existing, now) {
			open++
		}
	}
	if open >= maxUnlifted {
		return ReleaseFreeze{}, ErrReleaseFreezeLimit
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	f.Source = ReleaseFreezeSourceManual
	f.SourceDeploymentID = ""
	f.CreatedAt = now.UTC()
	f.LiftedAt, f.LiftedBy = nil, ""
	if m.releaseFreezes == nil {
		m.releaseFreezes = map[string]ReleaseFreeze{}
	}
	m.releaseFreezes[f.ID] = f
	return f, nil
}

func (m *MemStore) CreateRollbackReleaseFreeze(_ context.Context, f ReleaseFreeze) (bool, error) {
	if !f.Owner().Valid() || f.StartsAt == nil || f.EndsAt == nil {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.releaseFreezes {
		if existing.Source == ReleaseFreezeSourceAutomaticRollback && existing.SourceDeploymentID == f.SourceDeploymentID {
			return false, nil
		}
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	f.Kind = ReleaseFreezeWindow
	f.Source = ReleaseFreezeSourceAutomaticRollback
	f.CreatedAt = f.StartsAt.UTC()
	if m.releaseFreezes == nil {
		m.releaseFreezes = map[string]ReleaseFreeze{}
	}
	m.releaseFreezes[f.ID] = f
	return true, nil
}

func (m *MemStore) ListReleaseFreezes(_ context.Context, owner ReleaseFreezeOwner, now time.Time) ([]ReleaseFreeze, error) {
	if !owner.Valid() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReleaseFreeze
	for _, f := range m.releaseFreezes {
		if f.Owner() == owner && f.LiftedAt == nil && !releaseFreezeEnded(f, now) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) LiftReleaseFreeze(_ context.Context, owner ReleaseFreezeOwner, id, liftedBy string, now time.Time) (ReleaseFreeze, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.releaseFreezes[id]
	if !ok || !owner.Valid() || f.Owner() != owner || f.LiftedAt != nil {
		return ReleaseFreeze{}, ErrNotFound
	}
	at := now.UTC()
	f.LiftedAt, f.LiftedBy = &at, liftedBy
	m.releaseFreezes[id] = f
	return f, nil
}
