package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresCopyRetirementFixture(reader, target string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
	base, _ := state.PostgresTargetCompensationFixture("adopted", "adopted")
	c := base.Plan
	s, r, i := &c.Snapshot, &c.Restore, &c.Isolation.Plan
	s.OperationID, s.SourceDatabaseID, s.SourceVersion, s.BackendFingerprint = uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), strings.Repeat("b", 64)
	r.OperationID, r.SourceDatabaseID, r.AccountID, r.TargetOwnerID, r.BackendFingerprint = s.OperationID, s.SourceDatabaseID, uuid.NewString(), uuid.NewString(), s.BackendFingerprint
	r.AdoptedDatabaseID = r.TargetOwnerID
	i.OperationID, i.SourceDatabaseID, i.AccountID, i.OwnerID, i.BackendFingerprint = r.OperationID, r.SourceDatabaseID, r.AccountID, r.TargetOwnerID, s.BackendFingerprint
	p := state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan{SourceCompensationSHA256: strings.Repeat("c", 64), Capture: c, Scope: copyinventory.Scope{PostgresMajor: 16, ProjectID: uuid.NewString(), OperationID: r.OperationID, AccountID: r.AccountID, SourceDatabaseID: s.SourceDatabaseID, CaptureDatabaseID: r.AdoptedDatabaseID,
		SourceVersion: s.SourceVersion, BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, SourceProviderResourceID: s.SourceProviderResourceID, SourceDataResourceID: s.SourceDataResourceID,
		ProviderSnapshotID: s.ProviderSnapshotID, CaptureProviderResourceID: r.TargetProviderResourceID, CapturePoint: s.CapturePoint, SnapshotCreatedAt: s.SnapshotCreatedAt, CaptureCreatedAt: r.TargetCreatedAt}}
	at := s.CapturePoint
	if reader != "" {
		p.Reader = &state.ProjectEnvironmentClonePostgresCopyReader{Scope: p.Scope, OwnerID: uuid.NewString(), State: reader, DeletionOperations: "[]", CaptureDeletionOperations: "[]"}
		if reader != "reserved" {
			p.Reader.RequestStartedAt = at.Add(10 * time.Minute)
		}
		if reader == "observed" || reader == "deleting" || reader == "retired" {
			p.Reader.EndpointID, p.Reader.EndpointCreatedAt, p.Reader.ObservedAt = "reader-endpoint", at.Add(11*time.Minute), at.Add(11*time.Minute)
		}
		if reader == "deleting" || reader == "retired" {
			p.Reader.CleanupRequestedAt = at.Add(13 * time.Minute)
		}
		if reader == "retired" {
			p.Reader.RetiredAt = at.Add(14 * time.Minute)
		}
	}
	if target != "" {
		p.Target = &state.ProjectEnvironmentClonePostgresCopyTarget{OperationID: r.OperationID, SourceDatabaseID: s.SourceDatabaseID, AccountID: r.AccountID, CaptureDatabaseID: r.AdoptedDatabaseID, TargetDatabaseID: uuid.NewString(), State: target}
		if target != "reserved" {
			p.Target.RequestStartedAt = at.Add(10 * time.Minute)
		}
		if target == "preparing" || target == "prepared" || target == "deleting" || target == "retired" {
			p.Target.ProviderResourceID, p.Target.ProviderCreatedAt, p.Target.ObservedAt = "independent-copy-target", at.Add(11*time.Minute), at.Add(11*time.Minute)
		}
		if target == "prepared" {
			p.Target.PreparedAt = at.Add(12 * time.Minute)
		}
		if target == "deleting" || target == "retired" {
			p.Target.DeletionStartedAt = at.Add(13 * time.Minute)
		}
		if target == "retired" {
			p.Target.RetiredAt, p.Target.DeletionObservedAt = at.Add(14*time.Minute), at.Add(14*time.Minute)
		}
	}
	in := state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement{Plan: p, State: "requested", RequestedAt: at.Add(20 * time.Minute)}
	o := state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementRequestID(p), PlanSHA256: state.CloneCheckpointSHA(p), OwnerToken: p.Scope.OperationID, SourceDataResourceID: p.Scope.SourceDataResourceID, ProviderSnapshotID: p.Scope.ProviderSnapshotID, CaptureResourceID: p.Scope.CaptureProviderResourceID, NoNativeGrants: true, SourceUnchanged: true, SnapshotUnchanged: true, CaptureUnchanged: true, RetiredAt: at.Add(21 * time.Minute)}
	if p.Reader != nil {
		o.ReaderOwnerID, o.ReaderRequestID, o.ReaderEndpointID = p.Reader.OwnerID, p.Reader.OwnerID, p.Reader.EndpointID
		o.ReaderPermanentlyFenced, o.AllReaderOperationsPermanentlyClosed, o.NoOwnedReaders = true, true, true
	}
	if p.Target != nil {
		o.TargetOwnerID, o.TargetRequestID, o.TargetResourceID = p.Target.TargetDatabaseID, p.Target.TargetDatabaseID, p.Target.ProviderResourceID
		o.TargetPermanentlyFenced, o.AllWritersPermanentlyClosed, o.NoOwnedTargets = true, true, true
	}
	return in, o
}

func TestCheckpointPostgresCopyRetirementOriginalStates(t *testing.T) {
	for _, reader := range []string{"", "reserved", "requested", "observed", "deleting", "retired"} {
		for _, target := range []string{"", "reserved", "requested", "preparing", "prepared", "deleting", "retired"} {
			if reader == "" && target == "" {
				continue
			}
			t.Run(reader+"/"+target, func(t *testing.T) {
				in, o := postgresCopyRetirementFixture(reader, target)
				if err := state.ValidateCheckpointPostgresCopyRetirementObservation(in, o); err != nil {
					t.Fatal(err)
				}
				in.State, in.ObservationSHA256 = "retired", state.CloneCheckpointSHA(o)
				if err := state.ValidateCheckpointPostgresCopyRetirementObservation(in, o); err != nil {
					t.Fatal("terminal replay", err)
				}
				o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
				if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
					t.Fatal("changed terminal proof accepted")
				}
			})
		}
	}
}

func TestCheckpointPostgresCopyRetirementUnknownIdentities(t *testing.T) {
	for _, known := range []bool{false, true} {
		in, o := postgresCopyRetirementFixture("requested", "requested")
		if known {
			o.ReaderEndpointID, o.TargetResourceID = "observed-owned-reader", "observed-owned-target"
		}
		if err := state.ValidateCheckpointPostgresCopyRetirementObservation(in, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"reader", "target"} {
		in, o := postgresCopyRetirementFixture("reserved", "reserved")
		if role == "reader" {
			o.ReaderEndpointID = "discovered-reader"
		} else {
			o.TargetResourceID = "discovered-target"
		}
		if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
			t.Fatal("undispatched identity invented", role)
		}
	}
}

func TestCheckpointPostgresCopyRetirementRejectsIncompleteClosure(t *testing.T) {
	edits := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation){
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.RequestID += "foreign"
		},
		"digest": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.PlanSHA256 = strings.Repeat("f", 64)
		},
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.OwnerToken = uuid.NewString()
		},
		"reader fence": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.ReaderPermanentlyFenced = false
		},
		"queued reader cleanup": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.AllReaderOperationsPermanentlyClosed = false
		},
		"reader absence": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.NoOwnedReaders = false
		},
		"target fence": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.TargetPermanentlyFenced = false
		},
		"copy writer": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.AllWritersPermanentlyClosed = false
		},
		"target absence": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.NoOwnedTargets = false
		},
		"grants": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.NoNativeGrants = false
		},
		"source": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.SourceUnchanged = false
		},
		"snapshot": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.SnapshotUnchanged = false
		},
		"capture": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.CaptureUnchanged = false
		},
		"reader identity": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.ReaderEndpointID = "replacement"
		},
		"target identity": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.TargetResourceID = "replacement"
		},
		"reader request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.ReaderRequestID = uuid.NewString()
		},
		"target request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.TargetRequestID = uuid.NewString()
		},
		"capture identity": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.CaptureResourceID = "replacement"
		},
		"early time": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.RetiredAt = time.Time{}
		},
		"future time": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Add(time.Hour)
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			in, o := postgresCopyRetirementFixture("observed", "prepared")
			edit(&o)
			if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
				t.Fatal("incomplete closure accepted")
			}
		})
	}
}

func TestCheckpointPostgresCopyRetirementRejectsChangedOriginalPlan(t *testing.T) {
	edits := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan){
		"scope source": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Scope.SourceDatabaseID = uuid.NewString()
		},
		"reader scope": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Reader.Scope.PostgresMajor++
		},
		"reader owner": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Reader.OwnerID = p.Scope.CaptureDatabaseID
		},
		"target owner": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Target.TargetDatabaseID = p.Reader.OwnerID
		},
		"target capture": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Target.CaptureDatabaseID = uuid.NewString()
		},
		"reader source alias": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Reader.EndpointID = p.Scope.SourceDataResourceID
		},
		"target capture alias": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Target.ProviderResourceID = p.Scope.CaptureProviderResourceID
		},
		"no borrowers": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Reader, p.Target = nil, nil
		},
		"unknown capture": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) {
			p.Capture.Restore.State = "requested"
		},
		"missing isolation": func(p *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) { p.Capture.Isolation = nil },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			in, o := postgresCopyRetirementFixture("observed", "prepared")
			edit(&in.Plan)
			o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
			if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
				t.Fatal("changed original ownership accepted")
			}
		})
	}
	in, _ := postgresCopyRetirementFixture("requested", "requested")
	in.Plan.Capture.Snapshot.SourceVersion = strings.Repeat("x", api.PostgresCheckpointCopyRetirementMaxBytes)
	if !errors.Is(state.ValidateCheckpointPostgresCopyRetirementPlan(in.Plan), state.ErrQuotaExceeded) {
		t.Fatal("oversized original plan accepted")
	}
	// Round-tripping a saved original plan cannot replace its request identity.
	in, _ = postgresCopyRetirementFixture("requested", "requested")
	raw, err := json.Marshal(in.Plan)
	if err != nil {
		t.Fatal(err)
	}
	var replay state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan
	if json.Unmarshal(raw, &replay) != nil || state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlanSHA256(replay) != state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlanSHA256(in.Plan) {
		t.Fatal("saved original plan changed on replay")
	}
}

func TestCheckpointPostgresCopyRetirementOmittedRolesAndChronology(t *testing.T) {
	for _, role := range []string{"reader", "target"} {
		in, o := postgresCopyRetirementFixture("", "reserved")
		if role == "target" {
			in, o = postgresCopyRetirementFixture("reserved", "")
			o.TargetOwnerID, o.TargetRequestID, o.NoOwnedTargets = uuid.NewString(), uuid.NewString(), true
		} else {
			o.ReaderOwnerID, o.ReaderRequestID, o.NoOwnedReaders = uuid.NewString(), uuid.NewString(), true
		}
		if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
			t.Fatal("proof invented omitted role", role)
		}
	}
	in, o := postgresCopyRetirementFixture("reserved", "reserved")
	in.RequestedAt = in.Plan.Capture.Isolation.RequestedAt.Add(-time.Microsecond)
	if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
		t.Fatal("retirement preceded original isolation request")
	}
	in, o = postgresCopyRetirementFixture("requested", "requested")
	o.ReaderEndpointID, o.TargetResourceID = "one-physical-resource", "one-physical-resource"
	if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
		t.Fatal("reader and independent target shared physical identity")
	}
	for _, protected := range []string{in.Plan.Scope.SourceDataResourceID, in.Plan.Scope.SourceProviderResourceID, in.Plan.Scope.ProviderSnapshotID, in.Plan.Scope.CaptureProviderResourceID} {
		o.ReaderEndpointID, o.TargetResourceID = protected, ""
		if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
			t.Fatal("unknown reader aliased protected resource")
		}
		o.ReaderEndpointID, o.TargetResourceID = "", protected
		if state.ValidateCheckpointPostgresCopyRetirementObservation(in, o) == nil {
			t.Fatal("unknown target aliased protected resource")
		}
	}
}
