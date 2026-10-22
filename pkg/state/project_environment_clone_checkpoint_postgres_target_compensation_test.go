package state

import (
	"strings"
	"testing"
	"time"
)

func postgresTargetCompensationFixture(state, isolation string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	s := ProjectEnvironmentClonePostgresSnapshot{OperationID: "operation", SourceDatabaseID: "source", SourceVersion: "version", BackendID: "backend", BackendFingerprint: "fingerprint", SourceProviderResourceID: "source-cluster", SourceDataResourceID: "source-data", ProviderSnapshotID: "snapshot", State: "retained", CapturePoint: at, SnapshotCreatedAt: at}
	r := ProjectEnvironmentClonePostgresSnapshotRestore{OperationID: s.OperationID, SourceDatabaseID: s.SourceDatabaseID, AccountID: "account", TargetOwnerID: "target-owner", BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, State: state}
	if state != "reserved" {
		r.RequestStartedAt = at.Add(time.Minute)
	}
	if state != "reserved" && state != "requested" {
		r.TargetProviderResourceID = "target-data"
		r.TargetCreatedAt = at.Add(2 * time.Minute)
	}
	if state == "adopted" {
		r.AdoptedDatabaseID = r.TargetOwnerID
		r.AdoptedAt = at.Add(3 * time.Minute)
	}
	p := ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan{Snapshot: s, Restore: r}
	if isolation != "" {
		p.Isolation = &ProjectEnvironmentCloneCheckpointPostgresIsolation{Plan: ProjectEnvironmentCloneCheckpointPostgresIsolationPlan{OperationID: r.OperationID, AccountID: r.AccountID, SourceDatabaseID: r.SourceDatabaseID, OwnerID: r.TargetOwnerID, NativeResourceID: r.TargetProviderResourceID, BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, CheckpointID: "checkpoint", ProviderSnapshotID: s.ProviderSnapshotID, SourceDataResourceID: s.SourceDataResourceID, CapturePoint: s.CapturePoint, SnapshotCreatedAt: s.SnapshotCreatedAt, TargetCreatedAt: r.TargetCreatedAt}, State: isolation}
		if isolation != "planned" {
			p.Isolation.RequestedAt = at.Add(4 * time.Minute)
		}
		if isolation == "adopted" {
			p.Isolation.ObservationSHA256 = strings.Repeat("a", 64)
		}
	}
	in := ProjectEnvironmentCloneCheckpointPostgresTargetCompensation{Plan: p, State: "requested", RequestedAt: at.Add(5 * time.Minute)}
	o := ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation{RequestID: ProjectEnvironmentCloneCheckpointPostgresTargetCompensationRequestID(p), PlanSHA256: cloneCheckpointSHA(p), OwnerID: r.TargetOwnerID, RestoreRequestID: r.TargetOwnerID, TargetProviderResourceID: r.TargetProviderResourceID, RestorePermanentlyFenced: true, TargetAbsent: true, SourceUnchanged: true, SnapshotUnchanged: true, CompensatedAt: at.Add(6 * time.Minute)}
	if p.Isolation != nil {
		o.IsolationRequestID = ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(p.Isolation.Plan)
		o.IsolationPlanSHA256 = cloneCheckpointSHA(p.Isolation.Plan)
		o.IsolationPermanentlyFenced = true
	}
	return in, o
}

func TestCheckpointPostgresTargetCompensationOriginalStates(t *testing.T) {
	for _, state := range []string{"reserved", "requested", "restoring", "restored", "adopted", "deleting", "deleted"} {
		t.Run(state, func(t *testing.T) {
			in, o := postgresTargetCompensationFixture(state, "")
			if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State, in.ObservationSHA256 = "compensated", cloneCheckpointSHA(o)
			if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
				t.Fatal(err)
			}
			o.CompensatedAt = o.CompensatedAt.Add(time.Microsecond)
			if validateCheckpointPostgresTargetCompensationObservation(in, o) == nil {
				t.Fatal("accepted changed terminal proof")
			}
		})
	}
	for _, isolation := range []string{"planned", "requested", "adopted"} {
		t.Run("isolation-"+isolation, func(t *testing.T) {
			state := "restored"
			if isolation == "adopted" {
				state = "adopted"
			}
			in, o := postgresTargetCompensationFixture(state, isolation)
			if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
				t.Fatal(err)
			}
			o.IsolationPermanentlyFenced = false
			if validateCheckpointPostgresTargetCompensationObservation(in, o) == nil {
				t.Fatal("accepted delayed isolation authority")
			}
		})
	}
}

func TestCheckpointPostgresTargetCompensationBeforeAdoption(t *testing.T) {
	for _, isolation := range []string{"planned", "requested"} {
		in, o := postgresTargetCompensationFixture("restored", isolation)
		in.Plan.Restore.AdoptedDatabaseID = ""
		in.Plan.Restore.AdoptedAt = time.Time{}
		o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
		if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
			t.Fatal(isolation, err)
		}
	}
}

func TestCheckpointPostgresTargetCompensationUnknownRestore(t *testing.T) {
	for _, target := range []string{"", "discovered-owned-target"} {
		in, o := postgresTargetCompensationFixture("requested", "")
		o.TargetProviderResourceID = target
		if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
			t.Fatal(err)
		}
		o.RestorePermanentlyFenced = false
		if validateCheckpointPostgresTargetCompensationObservation(in, o) == nil {
			t.Fatal("absence accepted without permanent restore fence")
		}
	}
	in, o := postgresTargetCompensationFixture("deleting", "")
	in.Plan.Restore.RequestStartedAt = time.Time{}
	in.Plan.Restore.TargetProviderResourceID = ""
	in.Plan.Restore.TargetCreatedAt = time.Time{}
	o.TargetProviderResourceID = ""
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	if err := validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
		t.Fatal("unused reservation", err)
	}
}

func TestCheckpointPostgresTargetCompensationRejectsUnsafeProof(t *testing.T) {
	mutations := map[string]func(*ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation){
		"restore can run": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.RestorePermanentlyFenced = false
		},
		"target still exists": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.TargetAbsent = false
		},
		"source changed": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.SourceUnchanged = false
		},
		"snapshot changed": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.SnapshotUnchanged = false
		},
		"other owner": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.OwnerID = "other"
		},
		"other restore": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.RestoreRequestID = "other"
		},
		"other target": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.TargetProviderResourceID = "other"
		},
		"source target": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.TargetProviderResourceID = "source-data"
		},
		"other isolation": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.IsolationRequestID = "other"
		},
		"other isolation plan": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.IsolationPlanSHA256 = strings.Repeat("b", 64)
		},
		"planned compensation": func(in *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, _ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			in.State = "planned"
			in.RequestedAt = time.Time{}
		},
		"premature proof": func(in *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.CompensatedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future proof": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.CompensatedAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		},
		"other digest": func(_ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.PlanSHA256 = strings.Repeat("b", 64)
		},
		"wrong snapshot": func(in *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, _ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			in.Plan.Snapshot.ProviderSnapshotID = "other"
		},
		"wrong backend": func(in *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, _ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			in.Plan.Restore.BackendFingerprint = "other"
		},
		"wrong adoption": func(in *ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, _ *ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			in.Plan.Restore.AdoptedDatabaseID = "other"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in, o := postgresTargetCompensationFixture("adopted", "adopted")
			mutate(&in, &o)
			if validateCheckpointPostgresTargetCompensationObservation(in, o) == nil {
				t.Fatal("accepted unsafe cleanup proof")
			}
		})
	}
}
