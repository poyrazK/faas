package state

import (
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"strings"
	"testing"
	"time"
)

func sourceCompensationFixture(captured bool, releaseState string) (ProjectEnvironmentCloneCheckpointSourceCompensation, ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	hash := strings.Repeat("a", 64)
	c := ProjectEnvironmentCloneCheckpointPlan{OperationID: "operation", AccountID: "account", ProjectID: "project", SourceEnvironment: "production", SourceRevisionHash: hash, SourceReleaseSetID: "release-set", ConfigurationGeneration: 2, ConfigurationHeldAt: at, Sources: []ProjectEnvironmentCloneCheckpointSource{
		{Kind: "managed_postgres", ID: "database", BackendID: "pg", BackendFingerprint: "pg-fingerprint", ProviderResourceID: "cluster", DataResourceID: "physical-database", DefinitionSHA256: hash},
		{Kind: "object_storage", ID: "bucket", AppID: "app", Scope: "production", BackendID: "s3", BackendFingerprint: "s3-fingerprint", ProviderResourceID: "physical-bucket", DefinitionSHA256: hash},
	}}
	p := ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Capture: c, Intent: ProjectEnvironmentCloneResource{Kind: "data_checkpoint", Name: c.SourceEnvironment, SourceID: c.OperationID, SourceVersion: cloneCheckpointSHA(c), Status: "capturing"}}
	if captured {
		r := ProjectEnvironmentCloneCheckpointReceipt{RequestID: c.OperationID, PlanSHA256: cloneCheckpointSHA(c), CheckpointID: "checkpoint", CapturedAt: at.Add(time.Minute), CompleteWriterCoverage: true, Sources: []ProjectEnvironmentCloneCheckpointSourceReceipt{{Kind: c.Sources[0].Kind, ID: c.Sources[0].ID, CaptureIdentity: "snapshot"}, {Kind: c.Sources[1].Kind, ID: c.Sources[1].ID, CaptureIdentity: "manifest"}}}
		p.Receipt = &r
		p.Intent.Status = "captured"
		p.Intent.TargetID = r.CheckpointID
		p.Intent.CapturePoint = r.CapturedAt.Format(time.RFC3339Nano)
		if releaseState != "" {
			p.Release = &ProjectEnvironmentCloneCheckpointRelease{Plan: c, Receipt: r, AdoptionSHA256: hash, State: releaseState}
			if releaseState != "planned" {
				p.Release.RequestedAt = at.Add(2 * time.Minute)
			}
			if releaseState == "released" {
				p.Release.ObservationSHA256 = hash
			}
		}
	}
	in := ProjectEnvironmentCloneCheckpointSourceCompensation{Plan: p, State: "requested", RequestedAt: at.Add(3 * time.Minute)}
	o := ProjectEnvironmentCloneCheckpointSourceCompensationObservation{RequestID: ProjectEnvironmentCloneCheckpointSourceCompensationRequestID(c.OperationID), PlanSHA256: cloneCheckpointSHA(p), OwnerToken: c.OperationID, CaptureRequestID: c.OperationID, CapturePlanSHA256: cloneCheckpointSHA(c), CapturePermanentlyFenced: true, CompensatedAt: at.Add(5 * time.Minute)}
	if p.Release != nil {
		o.ReleaseRequestID = ProjectEnvironmentCloneCheckpointReleaseRequestID(c.OperationID)
		o.ReleasePlanSHA256 = cloneCheckpointSHA(c)
		o.ReleasePermanentlyFenced = true
	}
	for n, s := range c.Sources {
		resource := s.ProviderResourceID
		if s.Kind == "managed_postgres" {
			resource = s.DataResourceID
		}
		identity := ""
		if p.Receipt != nil {
			identity = p.Receipt.Sources[n].CaptureIdentity
		}
		o.Sources = append(o.Sources, ProjectEnvironmentCloneCheckpointCompensatedSource{Kind: s.Kind, ID: s.ID, SourceResourceID: resource, CaptureIdentity: identity, OwnerToken: c.OperationID, TerminalState: "abandoned", NoNativeHolds: true, SourceAccessRestored: true, SourceUnchanged: true, ReleasedAt: at.Add(4 * time.Minute)})
	}
	return in, o
}

func TestCheckpointSourceCompensationOriginalRequestStates(t *testing.T) {
	for _, state := range []string{"planned", "capturing", "captured", "release-planned", "release-requested", "release-released"} {
		t.Run(state, func(t *testing.T) {
			captured := state != "planned" && state != "capturing"
			release := strings.TrimPrefix(state, "release-")
			if release == state {
				release = ""
			}
			in, o := sourceCompensationFixture(captured, release)
			if state == "planned" {
				in.Plan.Intent.Status = "planned"
				o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
			}
			if err := validateCheckpointSourceCompensationObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State = "compensated"
			in.ObservationSHA256 = cloneCheckpointSHA(o)
			if err := validateCheckpointSourceCompensationObservation(in, o); err != nil {
				t.Fatal(err)
			}
			o.Sources[0].TerminalState = "released"
			if validateCheckpointSourceCompensationObservation(in, o) == nil {
				t.Fatal("accepted changed terminal replay")
			}
		})
	}
}

func TestCheckpointSourceCompensationRejectsIncompleteProof(t *testing.T) {
	mutations := map[string]func(*ProjectEnvironmentCloneCheckpointSourceCompensation, *ProjectEnvironmentCloneCheckpointSourceCompensationObservation){
		"capture can run": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.CapturePermanentlyFenced = false
		},
		"release can run": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.ReleasePermanentlyFenced = false
		},
		"foreign owner": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].OwnerToken = "other"
		},
		"foreign physical source": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].SourceResourceID = "other"
		},
		"source missing": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources = o.Sources[:1]
		},
		"source duplicated": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[1] = o.Sources[0]
		},
		"native hold remains": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].NoNativeHolds = false
		},
		"access not restored": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].SourceAccessRestored = false
		},
		"source changed": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].SourceUnchanged = false
		},
		"replacement capture": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].CaptureIdentity = "new"
		},
		"release before claim": func(in *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].ReleasedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future observation": func(_ *ProjectEnvironmentCloneCheckpointSourceCompensation, o *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.CompensatedAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		},
		"unclaimed": func(in *ProjectEnvironmentCloneCheckpointSourceCompensation, _ *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			in.State = "planned"
			in.RequestedAt = time.Time{}
		},
		"changed release": func(in *ProjectEnvironmentCloneCheckpointSourceCompensation, _ *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			in.Plan.Release.AdoptionSHA256 = "invalid"
		},
		"fabricated receipt": func(in *ProjectEnvironmentCloneCheckpointSourceCompensation, _ *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			in.Plan.Intent.Status = "capturing"
		},
		"missing coverage": func(in *ProjectEnvironmentCloneCheckpointSourceCompensation, _ *ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			in.Plan.Receipt.CompleteWriterCoverage = false
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in, o := sourceCompensationFixture(true, "requested")
			mutate(&in, &o)
			o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
			if validateCheckpointSourceCompensationObservation(in, o) == nil {
				t.Fatal("accepted incomplete compensation")
			}
		})
	}
	in, o := sourceCompensationFixture(false, "")
	o.ReleasePermanentlyFenced = true
	if validateCheckpointSourceCompensationObservation(in, o) == nil {
		t.Fatal("accepted invented release")
	}
}

func TestCheckpointSourceCompensationRejectsMalformedOriginalPlan(t *testing.T) {
	mutations := map[string]func(*ProjectEnvironmentCloneCheckpointSourceCompensationPlan){
		"unsorted sources": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.Sources[0], p.Capture.Sources[1] = p.Capture.Sources[1], p.Capture.Sources[0]
		},
		"duplicate source": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.Sources[1] = p.Capture.Sources[0]
		},
		"missing physical database": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.Sources[0].DataResourceID = ""
		},
		"missing object scope": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) { p.Capture.Sources[1].Scope = "" },
		"unsupported source": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.Sources[0].Kind = "unknown"
		},
		"invalid generation": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.ConfigurationGeneration = 1
		},
		"submicrosecond timestamp": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.ConfigurationHeldAt = p.Capture.ConfigurationHeldAt.Add(time.Nanosecond)
		},
		"nonutc timestamp": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) {
			p.Capture.ConfigurationHeldAt = p.Capture.ConfigurationHeldAt.In(time.FixedZone("other", 0))
		},
		"foreign intent": func(p *ProjectEnvironmentCloneCheckpointSourceCompensationPlan) { p.Intent.SourceID = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in, _ := sourceCompensationFixture(false, "")
			mutate(&in.Plan)
			in.Plan.Intent.SourceVersion = cloneCheckpointSHA(in.Plan.Capture)
			if validateCheckpointSourceCompensationPlan(in.Plan) == nil {
				t.Fatal("accepted malformed plan")
			}
		})
	}
}

func TestCheckpointSourceCompensationWithoutActiveReleaseSet(t *testing.T) {
	in, o := sourceCompensationFixture(false, "")
	in.Plan.Capture.SourceReleaseSetID = ""
	in.Plan.Intent.SourceVersion = cloneCheckpointSHA(in.Plan.Capture)
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	o.CapturePlanSHA256 = cloneCheckpointSHA(in.Plan.Capture)
	if err := validateCheckpointSourceCompensationObservation(in, o); err != nil {
		t.Fatal("valid capture without an active release set", err)
	}
}

func TestCheckpointSourceCompensationCanonicalReleaseJournalTime(t *testing.T) {
	in, _ := sourceCompensationFixture(true, "requested")
	plan, err := json.Marshal(in.Plan.Capture)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(in.Plan.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	original := in.Plan.Release
	row := sqlc.ProjectEnvironmentCloneCheckpointRelease{Plan: plan, Receipt: receipt, AdoptionSha256: original.AdoptionSHA256, State: original.State, RequestedAt: pgtype.Timestamptz{Time: original.RequestedAt.In(time.FixedZone("database driver", 3*60*60)), Valid: true}}
	canonical, err := cloneCheckpointReleaseFromSQL(row)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.RequestedAt.Location() != time.UTC || !canonical.RequestedAt.Equal(original.RequestedAt) {
		t.Fatal("release journal changed its instant or retained a non-UTC location")
	}
	in.Plan.Release = &canonical
	if err = validateCheckpointSourceCompensationPlan(in.Plan); err != nil {
		t.Fatal("valid database release journal rejected", err)
	}
}
