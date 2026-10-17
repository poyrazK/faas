package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Only durable control-plane observations are read here. GET and Preflight
// never dispatch provider I/O, install a fence, or choose a database selection.
func readPreflightSourceSessions(ctx context.Context, tx pgx.Tx, c Cutover, now time.Time) (*CutoverSourceSessions, string, time.Time, error) {
	f, err := readSourceFence(ctx, tx, c.AccountID, c.ID)
	if errors.Is(err, ErrNotFound) {
		digest := sha256.Sum256([]byte("source-fence-not-requested-v1"))
		return &CutoverSourceSessions{State: "not_requested", Blockers: []string{"source_fence_not_requested"}}, hex.EncodeToString(digest[:]), time.Time{}, nil
	}
	if err != nil {
		return nil, "", time.Time{}, err
	}
	q := sqlc.New()
	owner, err := q.ReadManagedPostgresDrainPlanOwner(ctx, tx, c.AppID)
	if err != nil {
		return nil, "", time.Time{}, mapPostgresError(err)
	}
	rows, err := q.ReadManagedPostgresPreflightDatabases(ctx, tx, []string{f.Source.ID})
	if err != nil {
		return nil, "", time.Time{}, mapPostgresError(err)
	}
	matched := false
	if len(rows) == 1 {
		d := databaseFromSQL(rows[0])
		matched = preflightDatabaseMatches(f.Source, d) && d.DataResourceID == f.Source.DataResourceID && d.Spec == f.Source.Spec
	}
	authority := cutoverUUID(owner.NodeID) == f.OwnerNodeID && owner.ScalingPolicyRevision == f.OwnerRevision &&
		cutoverUUID(owner.ManagedPostgresAdmissionCutoverID) == c.ID && owner.ManagedPostgresAdmissionFencedAt.Valid && owner.ManagedPostgresAdmissionFencedAt.Time.Equal(f.AdmissionFencedAt) &&
		(owner.Status == "active" || owner.Status == "evicted_cold") && (c.State == CutoverVerified || c.State == CutoverVerifying)
	// Refresh time and worker leases are not semantic evidence changes. Old
	// reports retain their own original observation expiry even after a refresh.
	raw, err := json.Marshal(struct {
		Owner                string
		Definition           RestoreSourceDefinition
		Maintenance          CheckpointMaintenance
		Stage, State         string
		CatalogFingerprint   string
		CatalogInvalidatedAt time.Time
		Names                []string
		Observation          *sourceFenceObservation
		Error                string
		Matched, Authority   bool
	}{f.OwnerToken, f.definition(), f.Maintenance, f.MaintenanceStage, f.State, f.CatalogFingerprint, f.CatalogInvalidatedAt, f.DatabaseNames, sourceWriterSemanticObservation(f.Observation), f.LastErrorCode, matched, authority})
	if err != nil {
		return nil, "", time.Time{}, err
	}
	digest := sha256.Sum256(raw)
	view := sourceSessionsView(f, now)
	if !matched {
		view.Blockers = append(view.Blockers, "source_changed")
	}
	if !authority {
		view.Blockers = append(view.Blockers, "admission_fence_changed")
	}
	if !matched || !authority {
		view.AdmissionClosed, view.SelectedDatabasesDrained = false, false
		view.WriterIsolation.State, view.WriterIsolation.Confirmed = "inactive", false
		view.WriterIsolation.CatalogStable, view.WriterIsolation.BackgroundWritersFenced = false, false
		view.Blockers = append(view.Blockers, "background_writers_unproven", "catalog_stability_unproven")
	}
	var expiry time.Time
	if !f.ObservedAt.IsZero() {
		expiry = f.ObservedAt.Add(CutoverVerificationMaxAge)
	}
	if f.State == "closed" && (f.LastErrorCode != "" || f.ObservedAt.IsZero() || now.Before(f.ObservedAt)) {
		expiry = now
	}
	if f.Observation != nil && f.Observation.WriterIsolation != nil {
		proofExpiry := f.Observation.WriterIsolation.ExpiresAt
		if !proofExpiry.IsZero() && (expiry.IsZero() || proofExpiry.Before(expiry)) {
			expiry = proofExpiry
		}
	}
	return view, hex.EncodeToString(digest[:]), expiry, nil
}
