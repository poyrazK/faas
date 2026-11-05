package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"time"
)

// CutoverPreflight is a durable observation, never permission to activate.
// Pending requests survive restarts; blocked requests can be explicitly retried.
type CutoverPreflight struct {
	CutoverID                                                      string
	State                                                          string
	Revision                                                       int64
	BindingIDs                                                     []string
	Blockers                                                       []string
	VerificationFresh, SourceMatches, TargetMatches, BindingsMatch bool
	CutoverUpdatedAt, CheckedAt, ExpiresAt, CreatedAt, UpdatedAt   time.Time
	RuntimeDrain                                                   *CutoverRuntimeDrain
	RuntimeDrainFingerprint                                        string
	// Recomputed from current runtime evidence on read; never persisted.
	RuntimeDrainChanged       bool
	SourceSessions            *CutoverSourceSessions
	SourceSessionsFingerprint string
	SourceSessionsChanged     bool
}

// CutoverRuntimeDrain describes only the scheduler's captured instance catalog.
// Catalog retirement is not proof of complete writer isolation.
type CutoverRuntimeDrain struct {
	State          string                      `json:"state"`
	InventoryCount int                         `json:"inventory_count"`
	Pending        int                         `json:"pending"`
	Retired        int                         `json:"retired"`
	Blocked        int                         `json:"blocked"`
	Blockers       []string                    `json:"blockers"`
	Members        []CutoverRuntimeDrainMember `json:"members"`
}

type CutoverRuntimeDrainMember struct {
	InstanceID   string `json:"instance_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Scope        string `json:"scope"`
	Mode         string `json:"mode"`
	State        string `json:"state"`
	BlockerCode  string `json:"blocker_code,omitempty"`
}

type CutoverPreflightStore interface {
	RequestCutoverPreflight(context.Context, string, string, time.Time) (CutoverPreflight, error)
	GetCutoverPreflight(context.Context, string, string) (CutoverPreflight, error)
	EvaluateCutoverPreflight(context.Context, string, string, time.Time) (CutoverPreflight, error)
	PendingCutoverPreflights(context.Context, int) ([]Cutover, error)
}

// These missing authorities must remain explicit even when all catalog checks
// pass. A VM catalog retirement receipt alone cannot establish writer safety.
func evaluateCutoverPreflight(c Cutover, source, target Database, bindings []Binding, targetBound bool, now time.Time) CutoverPreflight {
	p := CutoverPreflight{CutoverID: c.ID, State: "blocked", CheckedAt: now, ExpiresAt: now.Add(CutoverVerificationMaxAge), CutoverUpdatedAt: c.UpdatedAt,
		Blockers:   []string{"activation_disabled", "runtime_safety_unproven", "source_sessions_unfenced", "routes_unconverged", "snapshot_invalidation_unproven", "resume_plan_missing"},
		BindingIDs: []string{}}
	if c.State == CutoverCancelling || c.State.Closed() {
		p.Blockers = append(p.Blockers, "cutover_inactive")
	}
	p.SourceMatches = preflightDatabaseMatches(c.Source, source)
	p.TargetMatches = preflightDatabaseMatches(c.Target, target) && target.RestoreSourceDatabaseID == source.ID && target.RestoreSourceResourceID == databaseDataResource(source) && !targetBound
	if !p.SourceMatches {
		p.Blockers = append(p.Blockers, "source_changed")
	}
	if !p.TargetMatches {
		p.Blockers = append(p.Blockers, "target_changed")
	}
	p.VerificationFresh = c.VerificationFresh(now)
	p.BindingsMatch = len(c.Credentials) > 0 && len(bindings) == len(c.Credentials)
	for _, m := range c.Credentials {
		p.BindingIDs = append(p.BindingIDs, m.SourceBindingID)
		if m.State != "sealed" || !validSealedCredential(m.Sealed) {
			p.VerificationFresh = false
		}
		if !m.VerifiedAt.IsZero() && m.VerifiedAt.Add(CutoverVerificationMaxAge).Before(p.ExpiresAt) {
			p.ExpiresAt = m.VerifiedAt.Add(CutoverVerificationMaxAge)
		}
		matched := false
		for _, b := range bindings {
			if b.ID == m.SourceBindingID && b.AccountID == c.AccountID && b.DatabaseID == c.Source.ID && b.AppID == c.AppID && b.Scope == c.Scope && b.State == BindingStateReady && b.CredentialGeneration == m.SourceCredentialGeneration && b.EnvironmentKey == m.EnvironmentKey && b.Access == m.Access && b.RotationPreviousGeneration == 0 && b.LeaseToken == "" {
				matched = true
			}
		}
		p.BindingsMatch = p.BindingsMatch && matched
	}
	slices.Sort(p.BindingIDs)
	if !p.VerificationFresh {
		p.Blockers = append(p.Blockers, "verification_required")
	}
	if !p.BindingsMatch {
		p.Blockers = append(p.Blockers, "bindings_changed")
	}
	return p
}

func preflightDatabaseMatches(pin, current Database) bool {
	return current.State == StateReady && pin.ID == current.ID && pin.AccountID == current.AccountID && pin.BackendID == current.BackendID && pin.BackendFingerprint == current.BackendFingerprint && pin.ProviderResourceID != "" && pin.ProviderResourceID == current.ProviderResourceID && pin.DesiredGeneration == current.DesiredGeneration
}

func (s *CutoverService) RequestPreflight(ctx context.Context, account, id string) (CutoverPreflight, error) {
	store, ok := s.store.(CutoverPreflightStore)
	if !ok {
		return CutoverPreflight{}, ErrUnavailable
	}
	// Catalog inspection is useful even while provider provisioning is disabled.
	return store.RequestCutoverPreflight(ctx, account, id, s.bindings.now())
}

func (s *CutoverService) Preflight(ctx context.Context, account, id string) (CutoverPreflight, error) {
	store, ok := s.store.(CutoverPreflightStore)
	if !ok {
		return CutoverPreflight{}, ErrUnavailable
	}
	return store.GetCutoverPreflight(ctx, account, id)
}

func (s *CutoverService) sweepPreflights(ctx context.Context, limit int) error {
	store, ok := s.store.(CutoverPreflightStore)
	if !ok {
		return nil
	}
	rows, err := store.PendingCutoverPreflights(ctx, limit)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err = store.EvaluateCutoverPreflight(ctx, c.AccountID, c.ID, s.bindings.now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
