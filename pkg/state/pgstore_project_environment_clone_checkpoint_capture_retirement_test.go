//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func captureRetirementDigest(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func pgCaptureRetirementFixture(t *testing.T, phase string) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease) {
	t.Helper()
	s, ctx, pool := pgWithPool(t)
	a, project, app, legacy := cloneBindingFixture(t, s)
	secret := clonePostgresSecretFixture(t, pool, a, app, "production", "capture-retirement-source", 1)
	if err := s.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if phase == "adopted-mixed" {
		cloneBindingBucket(t, s, a, app, "retained-assets", "production", true)
	}
	// The binding helper's legacy operation has no capture or native authority.
	// Finish it before creating a full configuration capture for this protocol.
	var err error
	legacy, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, project.ID, legacy.ID, legacy.Status, state.CloneOperationCompensating, legacy.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, project.ID, legacy.ID, legacy.Status, state.CloneOperationCompensated, legacy.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "capture-retirement"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || l.Operation.ID != op.ID {
		t.Fatal("capture claim", err)
	}
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, l.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	guard, err := s.AcquireProjectEnvironmentCloneConfigurationFence(ctx, l)
	if err != nil {
		t.Fatal("configuration guard", err)
	}
	views, err := s.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	db := views[0].Postgres[0]
	id := db.DatabaseID
	if _, err = s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	hash, err := state.ProjectEnvironmentCloneDatabaseSourceHash(db)
	if err != nil {
		t.Fatal(err)
	}
	op = l.Operation
	c := state.ProjectEnvironmentCloneCheckpointPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: op.SourceReleaseSetID, ConfigurationGeneration: guard.Generation, ConfigurationHeldAt: guard.HeldAt.UTC(), Sources: []state.ProjectEnvironmentCloneCheckpointSource{{Kind: "managed_postgres", ID: db.DatabaseID, BackendID: db.BackendID, BackendFingerprint: db.BackendFingerprint, ProviderResourceID: db.ProviderResourceID, DataResourceID: db.DataResourceID, DefinitionSHA256: hash}}}
	for _, bucket := range views[0].Buckets {
		if _, err = s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, bucket.ID); err != nil {
			t.Fatal(err)
		}
		c.Sources = append(c.Sources, state.ProjectEnvironmentCloneCheckpointSource{Kind: "object_storage", ID: bucket.ID, AppID: views[0].AppID, Scope: views[0].SourceScope, BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, ProviderResourceID: bucket.PhysicalName, DefinitionSHA256: captureRetirementDigest(t, bucket)})
	}
	i := state.ProjectEnvironmentCloneResource{Kind: "data_checkpoint", Name: op.SourceEnvironment, SourceID: op.ID, SourceVersion: captureRetirementDigest(t, c), Status: "capturing"}
	if phase == "planned" {
		i.Status = "planned"
	}
	p := state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Capture: c, Intent: i}
	resources := []state.ProjectEnvironmentCloneResource{i}
	var adoption state.ProjectEnvironmentCloneCheckpointAdoption
	if phase == "captured" || strings.HasPrefix(phase, "adopted") {
		at := time.Now().UTC().Truncate(time.Microsecond)
		p.Receipt = &state.ProjectEnvironmentCloneCheckpointReceipt{RequestID: op.ID, PlanSHA256: i.SourceVersion, CheckpointID: "original-checkpoint", CapturedAt: at, CompleteWriterCoverage: true, Sources: []state.ProjectEnvironmentCloneCheckpointSourceReceipt{{Kind: "managed_postgres", ID: id, CaptureIdentity: "original-snapshot"}}}
		for _, bucket := range views[0].Buckets {
			items := []state.ProjectEnvironmentCloneObjectVersion{{Key: "asset.txt", VersionID: "original-version", Size: 12, LastModified: at}}
			manifestHash, err := state.ProjectEnvironmentCloneObjectManifestHash(items)
			if err != nil {
				t.Fatal(err)
			}
			p.Receipt.Sources = append(p.Receipt.Sources, state.ProjectEnvironmentCloneCheckpointSourceReceipt{Kind: "object_storage", ID: bucket.ID, CaptureIdentity: manifestHash})
			adoption.Manifests = append(adoption.Manifests, state.ProjectEnvironmentCloneCheckpointManifest{SourceBucketID: bucket.ID, Hash: manifestHash, CapturedAt: at, Objects: items, RetainedUntil: at.Add(time.Hour)})
		}
		adoption.Receipt = *p.Receipt
		adoption.Snapshots = []state.ProjectEnvironmentCloneCheckpointSnapshot{{SourceDatabaseID: id, RequestStartedAt: at, Observation: state.ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: "original-snapshot", SourceDataResourceID: db.DataResourceID, CapturePoint: at, CreatedAt: at}}}
		i.Status, i.TargetID, i.CapturePoint = "captured", p.Receipt.CheckpointID, at.Format(time.RFC3339Nano)
		p.Intent = i
		resources = []state.ProjectEnvironmentCloneResource{i, {Kind: "data_checkpoint_receipt", Name: i.Name, SourceID: i.TargetID, SourceVersion: captureRetirementDigest(t, p.Receipt), CapturePoint: i.CapturePoint, Status: "captured"}}
	}
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(phase, "adopted") {
		l.Operation, err = s.AdoptProjectEnvironmentCloneCheckpoint(ctx, l, adoption, 10)
		if err != nil {
			t.Fatal("adopt original capture", err)
		}
		resources = l.Operation.Resources
	}
	op = l.Operation
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing source compensation accepted", err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, p); err != nil {
		t.Fatal("source reserve", err)
	}
	source, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l)
	if err != nil || !dispatch {
		t.Fatal("source claim", err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("pending source compensation accepted", err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	proof := state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointSourceCompensationRequestID(op.ID), PlanSHA256: captureRetirementDigest(t, source.Plan), OwnerToken: op.ID, CaptureRequestID: op.ID, CapturePlanSHA256: captureRetirementDigest(t, c), CapturePermanentlyFenced: true, CompensatedAt: at}
	for n, original := range c.Sources {
		physical, identity := original.ProviderResourceID, ""
		if original.Kind == "managed_postgres" {
			physical = original.DataResourceID
		}
		if p.Receipt != nil {
			identity = p.Receipt.Sources[n].CaptureIdentity
		}
		proof.Sources = append(proof.Sources, state.ProjectEnvironmentCloneCheckpointCompensatedSource{Kind: original.Kind, ID: original.ID, SourceResourceID: physical, CaptureIdentity: identity, OwnerToken: op.ID, TerminalState: "abandoned", NoNativeHolds: true, SourceAccessRestored: true, SourceUnchanged: true, ReleasedAt: at})
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, proof); err != nil {
		t.Fatal("source finish", err)
	}
	return s, pool, l
}

func pgCaptureRetirementProof(t *testing.T, in state.ProjectEnvironmentCloneCheckpointCaptureRetirement) state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation {
	t.Helper()
	s := in.Plan.SourceCompensation
	c := s.Plan.Capture
	o := state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(c.OperationID), PlanSHA256: captureRetirementDigest(t, in.Plan), OwnerToken: c.OperationID, CaptureRequestID: c.OperationID, CapturePlanSHA256: captureRetirementDigest(t, c), CapturePermanentlyFenced: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond)}
	for _, source := range s.Observation.Sources {
		o.Sources = append(o.Sources, state.ProjectEnvironmentCloneCheckpointRetiredCapture{Kind: source.Kind, ID: source.ID, SourceResourceID: source.SourceResourceID, CaptureIdentity: source.CaptureIdentity, OwnerToken: source.OwnerToken, NoOwnedArtifacts: true, NoNativeBorrowers: true, SourceUnchanged: true, SourceAccessRestored: true})
	}
	return o
}

func TestPgCheckpointCaptureRetirementRecoveryAndTerminalReplay(t *testing.T) {
	for _, phase := range []string{"planned", "unknown", "captured", "adopted"} {
		t.Run(phase, func(t *testing.T) {
			s, pool, l := pgCaptureRetirementFixture(t, phase)
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil || in.State != "planned" {
				t.Fatal("reserve", err)
			}
			if replay, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil || captureRetirementDigest(t, replay) != captureRetirementDigest(t, in) {
				t.Fatal("reserve replay", err)
			}
			in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil || !dispatch || in.State != "requested" {
				t.Fatal("first dispatch", err)
			}
			// Treat the committed claim reply as lost. A new lease must observe the
			// original request, even if the first worker never dispatched native IO.
			old := l
			if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, old); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("stale worker", err)
			}
			in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil || dispatch || in.State != "requested" {
				t.Fatal("recovery redispatched", err)
			}
			o := pgCaptureRetirementProof(t, in)
			bad := o
			bad.CapturePermanentlyFenced = false
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, bad); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unfenced proof", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(cancelled, l, o); err == nil {
				t.Fatal("cancelled finish accepted")
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err != nil {
				t.Fatal("finish", err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err != nil {
				t.Fatal("lost finish replay", err)
			}
			o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed terminal proof", err)
			}
			in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil || dispatch || in.State != "retired" {
				t.Fatal("completed recovery", err)
			}
			op, err := s.ProjectEnvironmentCloneOperationByID(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil || op.Status != state.CloneOperationCompensating {
				t.Fatal("whole stage falsely completed", err)
			}
		})
	}
}

func TestPgCheckpointCaptureRetirementMigrationAndChangedOwnership(t *testing.T) {
	s, pool, l := pgCaptureRetirementFixture(t, "unknown")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006383_clone_checkpoint_capture_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := migrations.FS.ReadFile("20261011232006385_clone_checkpoint_capture_catalogue_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, strings.SplitN(string(newer), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("empty newer catalogue downgrade", err)
	}
	dropEmptyCloneCompensationReceiptForParentMigration(t, tx)
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("empty migration round trip", err)
		}
	}
	_ = tx.Rollback(ctx)
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"planned", "requested", "retired"} {
		if phase == "requested" {
			if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil {
				t.Fatal(err)
			}
		} else if phase == "retired" {
			in, err := s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, pgCaptureRetirementProof(t, in)); err != nil {
				t.Fatal(err)
			}
		}
		tx, err = pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, parts[1])
		_ = tx.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_capture_retirements_down_no_ownership" {
			t.Fatal("owned migration downgrade", phase, err)
		}
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_source_compensations SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed source evidence accepted", err)
	}
}

func TestPgCheckpointCaptureRetirementBlocksAdoptionAndRollsBackFinish(t *testing.T) {
	s, pool, l := pgCaptureRetirementFixture(t, "unknown")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	source := in.Plan.SourceCompensation.Plan.Capture.Sources[0]
	// An inconsistent partial adoption must block dispatch independently of the
	// operation's resource labels, even when no target has yet been created.
	if _, err = pool.Exec(ctx, `INSERT INTO project_environment_clone_postgres_snapshots
	 (operation_id,source_database_id,source_version,backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id,capture_point)
	 VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp())`, l.Operation.ID, source.ID, source.DefinitionSHA256, source.BackendID, source.BackendFingerprint, source.ProviderResourceID, source.DataResourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("adoption accepted on reserve replay", err)
	}
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("adoption authorized dispatch", err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM project_environment_clone_postgres_snapshots WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil || !dispatch {
		t.Fatal(err)
	}
	o := pgCaptureRetirementProof(t, in)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_capture_retirement_finish() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN IF NEW.state='retired' THEN RAISE EXCEPTION 'injected capture retirement finish failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_capture_retirement_finish BEFORE UPDATE ON project_environment_clone_checkpoint_capture_retirements FOR EACH ROW EXECUTE FUNCTION reject_capture_retirement_finish();`); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err == nil {
		t.Fatal("failed terminal write accepted")
	}
	in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil || dispatch || in.State != "requested" || in.ObservationSHA256 != "" {
		t.Fatal("failed finish lost authority or redispatched", err)
	}
	if _, err = pool.Exec(ctx, "DROP TRIGGER reject_capture_retirement_finish ON project_environment_clone_checkpoint_capture_retirements; DROP FUNCTION reject_capture_retirement_finish();"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err != nil {
		t.Fatal(err)
	}
}
