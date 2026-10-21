package managedpostgres

import (
	"context"
	"errors"
	"time"
)

// The private alias preserves evidence in the operational journal. The public
// provider value redacts JSON; only this explicitly owned journal serializes it.
type sourceWriterIsolationEvidence CheckpointWriterIsolationEvidence

// CutoverWriterIsolation is a historical diagnostic and provider attestation.
// Confirmed does not qualify a deployed provider or permit customer activation.
type CutoverWriterIsolation struct {
	State                   string `json:"state"`
	Confirmed               bool   `json:"confirmed"`
	InventoryAvailable      bool   `json:"inventory_available"`
	CatalogMatches          bool   `json:"catalog_matches"`
	CatalogStable           bool   `json:"catalog_stable"`
	BackgroundWritersFenced bool   `json:"background_writers_fenced"`
	ClientSessions          int64  `json:"client_sessions"`
	BackgroundWorkers       int64  `json:"background_workers"`
	ReplicationWorkers      int64  `json:"replication_workers"`
	UnknownBackends         int64  `json:"unknown_backends"`
	ExpiresAt               string `json:"expires_at,omitempty"`
}

func sourceWriterCatalogComplete(f CutoverSourceFence) bool {
	return f.Observation != nil && f.Observation.WriterInventory != nil &&
		f.Observation.WriterInventory.Validate() == nil && f.Observation.WriterInventory.CatalogComplete &&
		f.Observation.UnselectedDatabases == 0 && f.Observation.WriterInventory.DatabaseCount == len(f.DatabaseNames)+1
}

func (f CutoverSourceFence) writerIsolationRequest() CheckpointWriterIsolationRequest {
	r := CheckpointWriterIsolationRequest{Identity: f.identity(), Definition: f.definition(), Generation: f.Source.DesiredGeneration,
		Maintenance: f.Maintenance, CatalogFingerprint: f.CatalogFingerprint}
	if f.Observation != nil {
		r.ClosedAt = f.Observation.ClosedAt
	}
	return r
}

func sourceWriterInventoryIdle(f CutoverSourceFence) bool {
	if !sourceWriterCatalogComplete(f) || !f.Observation.Drained {
		return false
	}
	i := f.Observation.WriterInventory
	return i.ClientSessions == 0 && i.BackgroundWorkers == 0 && i.ReplicationWorkers == 0 && i.UnknownBackends == 0
}

// Evidence reads share the closure call's provider deadline. Failure to obtain
// stronger evidence never discards a successfully observed SQL closure.
func (s *CutoverService) observeSourceWriterIsolation(ctx context.Context, before CutoverSourceFence, next *CutoverSourceFence) {
	o := next.Observation
	if before.Observation != nil && before.Observation.WriterIsolation != nil {
		retained := *before.Observation.WriterIsolation
		o.WriterIsolation = &retained
	}
	if !next.CatalogInvalidatedAt.IsZero() {
		o.WriterIsolationCode = "source_writer_catalog_changed"
		return
	}
	b, err := s.checkpoint.checkpointBackend(next.definition())
	if err != nil {
		o.WriterIsolationCode = "source_writer_isolation_unavailable"
		return
	}
	if _, ok := b.Provider.(CheckpointWriterIsolationProvider); !ok {
		o.WriterIsolationCode = "source_writer_isolation_unsupported"
		return
	}
	if !sourceWriterCatalogComplete(*next) {
		o.WriterIsolationCode = "source_writer_inventory_incomplete"
		return
	}
	if next.CatalogFingerprint == "" || o.WriterInventory.CatalogFingerprint != next.CatalogFingerprint {
		o.WriterIsolationCode = "source_writer_catalog_changed"
		return
	}
	if !sourceWriterInventoryIdle(*next) {
		o.WriterIsolationCode = "source_writers_active"
		return
	}
	evidence, err := s.checkpoint.ObserveCheckpointWriterIsolation(ctx, next.writerIsolationRequest())
	if err != nil {
		switch {
		case errors.Is(err, ErrUnsupported):
			o.WriterIsolationCode = "source_writer_isolation_unsupported"
		case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalid):
			o.WriterIsolationCode = "source_writer_isolation_invalid"
		default:
			o.WriterIsolationCode = "source_writer_isolation_unavailable"
		}
		return
	}
	retained := sourceWriterIsolationEvidence(evidence)
	o.WriterIsolation, o.WriterIsolationCode = &retained, ""
}

func sourceWriterIsolationView(f CutoverSourceFence, now time.Time, blockers []string) (*CutoverWriterIsolation, []string) {
	v := &CutoverWriterIsolation{State: "pending"}
	if f.Observation != nil {
		o := f.Observation
		if i := o.WriterInventory; i != nil && i.Validate() == nil {
			v.InventoryAvailable = true
			v.ClientSessions, v.BackgroundWorkers, v.ReplicationWorkers, v.UnknownBackends = i.ClientSessions, i.BackgroundWorkers, i.ReplicationWorkers, i.UnknownBackends
			v.CatalogMatches = sourceWriterCatalogComplete(f) && f.CatalogFingerprint != "" && f.CatalogInvalidatedAt.IsZero() && i.CatalogFingerprint == f.CatalogFingerprint
		}
		if !v.InventoryAvailable {
			blockers = append(blockers, "source_writer_inventory_unavailable")
		} else if !v.CatalogMatches {
			blockers = append(blockers, "source_writer_catalog_changed")
		}
		for _, finding := range []struct {
			count int64
			code  string
		}{{v.ClientSessions, "source_writer_clients_active"}, {v.BackgroundWorkers, "source_background_workers_active"},
			{v.ReplicationWorkers, "source_replication_workers_active"}, {v.UnknownBackends, "source_backend_visibility_incomplete"}} {
			if finding.count > 0 {
				blockers = append(blockers, finding.code)
			}
		}
		v.State = "blocked"
		if o.WriterIsolation != nil {
			e := CheckpointWriterIsolationEvidence(*o.WriterIsolation)
			v.ExpiresAt = e.ExpiresAt.UTC().Format(time.RFC3339Nano)
			err := e.Validate(f.writerIsolationRequest(), now)
			freshSQL := !f.ObservedAt.IsZero() && !now.Before(f.ObservedAt) && now.Before(f.ObservedAt.Add(CutoverVerificationMaxAge))
			if err == nil && o.WriterIsolationCode == "" && f.LastErrorCode == "" && freshSQL && v.CatalogMatches && sourceWriterInventoryIdle(f) && f.State == "closed" {
				v.State, v.Confirmed, v.CatalogStable, v.BackgroundWritersFenced = "confirmed", true, true, true
			} else if errors.Is(err, ErrUnavailable) {
				v.State = "expired"
				blockers = append(blockers, "source_writer_isolation_expired")
			} else if err != nil {
				blockers = append(blockers, "source_writer_isolation_invalid")
			}
		}
		if o.WriterIsolationCode != "" {
			blockers = append(blockers, o.WriterIsolationCode)
			switch o.WriterIsolationCode {
			case "source_writer_isolation_unsupported":
				v.State = "unsupported"
			case "source_writer_isolation_unavailable", "source_writer_inventory_incomplete":
				v.State = "unavailable"
			}
		}
	}
	if f.State == "abandoning" || f.State == "released" {
		v.State = "inactive"
	}
	if !v.BackgroundWritersFenced {
		blockers = append(blockers, "background_writers_unproven")
	}
	if !v.CatalogStable {
		blockers = append(blockers, "catalog_stability_unproven")
	}
	return v, blockers
}

// Local refresh timestamps are not semantic changes. Provider evidence expiry
// remains in the digest so shortened or renewed attestation windows invalidate
// historical reports rather than extending their original lifetime silently.
func sourceWriterSemanticObservation(o *sourceFenceObservation) *sourceFenceObservation {
	if o == nil {
		return nil
	}
	copyObservation := *o
	if o.WriterIsolation != nil {
		e := *o.WriterIsolation
		e.ObservedAt = time.Time{}
		copyObservation.WriterIsolation = &e
	}
	return &copyObservation
}
