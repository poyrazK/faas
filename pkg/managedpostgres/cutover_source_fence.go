package managedpostgres

import (
	"context"
	"math"
	"slices"
	"time"
)

// CutoverSourceFence is private recovery authority, not an activation token.
// Its operation token is independent of both the cutover and maintenance owner.
type CutoverSourceFence struct {
	CutoverID, AccountID, AppID, OwnerToken string
	Source                                  Database
	OwnerNodeID                             string
	OwnerRevision                           int64
	AdmissionFencedAt                       time.Time
	RuntimeFingerprint                      string
	CatalogFingerprint                      string
	CatalogInvalidatedAt                    time.Time
	Maintenance                             CheckpointMaintenance
	MaintenanceStage                        string
	State                                   string
	DatabaseNames                           []string
	CloseRequestedAt, ObservedAt            time.Time
	Observation                             *sourceFenceObservation
	LastErrorCode, RemoteTerminalState      string
	RemoteReleasedAt                        time.Time
	LeaseToken                              string
	LeaseUntil                              time.Time
	AttemptCount                            int32
	RetryAt, CreatedAt, UpdatedAt           time.Time
}

func (CutoverSourceFence) String() string     { return "[private cutover SQL fence]" }
func (f CutoverSourceFence) GoString() string { return f.String() }
func (CutoverSourceFence) MarshalJSON() ([]byte, error) {
	return []byte(`"[private cutover SQL fence]"`), nil
}

type sourceFenceObservation struct {
	ClosedAt            time.Time                      `json:"closed_at"`
	Databases           []CheckpointConnectionDatabase `json:"databases"`
	UnselectedDatabases int64                          `json:"unselected_databases"`
	Drained             bool                           `json:"drained"`
	WriterInventory     *CheckpointWriterInventory     `json:"writer_inventory,omitempty"`
	WriterIsolation     *sourceWriterIsolationEvidence `json:"writer_isolation,omitempty"`
	WriterIsolationCode string                         `json:"writer_isolation_code,omitempty"`
}

func (f CutoverSourceFence) definition() RestoreSourceDefinition {
	return RestoreSourceDefinition{Spec: f.Source.Spec, BackendID: f.Source.BackendID, BackendFingerprint: f.Source.BackendFingerprint,
		ProviderResourceID: f.Source.ProviderResourceID, DataResourceID: f.Source.DataResourceID}
}
func (f CutoverSourceFence) identity() CheckpointConnectionIdentity {
	return CheckpointConnectionIdentity{OwnerToken: f.OwnerToken, SourceResourceID: f.Source.DataResourceID}
}
func (f CutoverSourceFence) request() CheckpointConnectionRequest {
	return CheckpointConnectionRequest{CheckpointConnectionIdentity: f.identity(), DatabaseNames: slices.Clone(f.DatabaseNames)}
}

type CutoverSourceFenceStore interface {
	ReserveCutoverSourceFence(context.Context, string, string, time.Time) (CutoverSourceFence, error)
	GetCutoverSourceFence(context.Context, string, string) (CutoverSourceFence, error)
	ClaimCutoverSourceFence(context.Context, string, string, string, time.Time, time.Time) (CutoverSourceFence, error)
	SaveCutoverSourceFence(context.Context, CutoverSourceFence, CutoverSourceFence, bool, time.Time, time.Time) (CutoverSourceFence, error)
	DueCutoverSourceFences(context.Context, bool, int, time.Time) ([]Cutover, error)
}

// CutoverSourceSessions contains safe historical findings only. Zero session
// counts without a confirmed, fresh closure never establish writer isolation.
type CutoverSourceSessions struct {
	State                    string                  `json:"state"`
	AdmissionClosed          bool                    `json:"admission_closed"`
	SelectedDatabasesDrained bool                    `json:"selected_databases_drained"`
	SelectedDatabaseCount    int                     `json:"selected_database_count"`
	Sessions                 int64                   `json:"sessions"`
	PreparedTransactions     int64                   `json:"prepared_transactions"`
	UnselectedDatabases      int64                   `json:"unselected_databases"`
	Blockers                 []string                `json:"blockers"`
	ObservedAt               string                  `json:"observed_at,omitempty"`
	WriterIsolation          *CutoverWriterIsolation `json:"writer_isolation,omitempty"`
}

func sourceSessionsView(f CutoverSourceFence, now time.Time) *CutoverSourceSessions {
	o := &CutoverSourceSessions{State: f.State, SelectedDatabaseCount: len(f.DatabaseNames), Blockers: []string{}}
	switch f.State {
	case "pending":
		if f.MaintenanceStage != "ready" {
			o.Blockers = append(o.Blockers, "maintenance_not_ready")
		} else if len(f.DatabaseNames) == 0 {
			o.Blockers = append(o.Blockers, "database_selection_missing")
		} else {
			o.Blockers = append(o.Blockers, "admission_closure_unconfirmed")
		}
	case "closing":
		o.Blockers = append(o.Blockers, "admission_closure_unconfirmed")
	case "abandoning", "released":
		o.Blockers = append(o.Blockers, "source_fence_inactive")
	}
	if f.Observation != nil {
		o.SelectedDatabaseCount = len(f.Observation.Databases)
		o.UnselectedDatabases = f.Observation.UnselectedDatabases
		for _, db := range f.Observation.Databases {
			o.Sessions += db.Sessions
			o.PreparedTransactions += db.PreparedTransactions
		}
		o.ObservedAt = f.ObservedAt.UTC().Format(time.RFC3339Nano)
		fresh := !f.ObservedAt.IsZero() && !now.Before(f.ObservedAt) && now.Before(f.ObservedAt.Add(CutoverVerificationMaxAge))
		o.AdmissionClosed = f.State == "closed" && f.LastErrorCode == "" && fresh
		o.SelectedDatabasesDrained = o.AdmissionClosed && f.Observation.Drained
		if !fresh {
			o.Blockers = append(o.Blockers, "source_fence_observation_expired")
		}
		if o.Sessions != 0 {
			o.Blockers = append(o.Blockers, "source_sessions_active")
		}
		if o.PreparedTransactions != 0 {
			o.Blockers = append(o.Blockers, "prepared_transactions_pending")
		}
		if o.UnselectedDatabases != 0 {
			o.Blockers = append(o.Blockers, "database_coverage_incomplete")
		}
	}
	if f.LastErrorCode != "" {
		o.Blockers = append(o.Blockers, f.LastErrorCode, "source_fence_observation_unavailable")
	}
	o.WriterIsolation, o.Blockers = sourceWriterIsolationView(f, now, o.Blockers)
	return o
}

func sameSourceFenceClosurePins(a, b *sourceFenceObservation) bool {
	if a == nil || b == nil || !a.ClosedAt.Equal(b.ClosedAt) || len(a.Databases) != len(b.Databases) {
		return false
	}
	for i, x := range a.Databases {
		y := b.Databases[i]
		if x.Name != y.Name || x.OID != y.OID || x.OwnerOID != y.OwnerOID || x.OriginalAllowConnections != y.OriginalAllowConnections {
			return false
		}
	}
	return true
}

func sourceMaintenanceStageIndex(stage string) int {
	return slices.Index([]string{"reserved", "role_requested", "role_reserved", "database_requested", "database_created", "activation_requested", "ready"}, stage)
}

func validateSourceFenceObservation(f CutoverSourceFence) error {
	if f.Observation == nil {
		return ErrConflict
	}
	o := CheckpointConnectionClosure{CheckpointConnectionIdentity: f.identity(), State: "closed", ClosedAt: f.Observation.ClosedAt,
		Databases: f.Observation.Databases, Drained: f.Observation.Drained, UnselectedDatabases: f.Observation.UnselectedDatabases}
	o.WriterInventory = f.Observation.WriterInventory
	if err := o.Validate(f.request()); err != nil {
		return err
	}
	var sessions, prepared int64
	for _, db := range o.Databases {
		if db.Sessions > math.MaxInt64-sessions || db.PreparedTransactions > math.MaxInt64-prepared {
			return ErrConflict
		}
		sessions += db.Sessions
		prepared += db.PreparedTransactions
	}
	return nil
}
