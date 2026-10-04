//go:build !no_pg

// adr:375
package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneDataWorkerFixture(t *testing.T) (*verificationWorkerFixture, clonePostgresDataWorkConfig) {
	t.Helper()
	f := cloneImportWorkerFixtureWithPreparation(t, false)
	v := &verificationWorkerFixture{f: f, store: &verificationWorkerFailureStore{importWorkerFailureStore: f.store}, cfg: cloneContentsReadConfig(t)}
	x := f.db.x
	x.f.srv.store, x.f.srv.clonePostgresContentsReadPool = v.store, v.cfg.ReadPool
	cfg := clonePostgresDataWorkConfig{Artifact: f.artifact, PGDump: os.Getenv("FAAS_COPY_PG_DUMP"), PGRestore: f.tool, ScratchRoot: t.TempDir(), MaxPlainBytes: 4 << 20,
		ArchiveBytes: 8 << 20, ArchiveLimits: archiveWorkerLimits(), ContentsBytes: api.PostgresCopyCiphertextMaxBytes, ContentsLimits: contentsWorkerLimits(), Read: v.cfg}
	return v, cfg
}

func runCloneDataWorker(t *testing.T, v *verificationWorkerFixture, cfg clonePostgresDataWorkConfig) (clonePostgresDataWorkProgress, error) {
	t.Helper()
	x := v.f.db.x
	return x.f.srv.processProjectEnvironmentClonePostgresData(t.Context(), x.f.lease, x.source, v.f.db.exports, v.f.sourceOID, cfg)
}

func prepareCloneDataWorkerThroughImport(t *testing.T, v *verificationWorkerFixture, cfg clonePostgresDataWorkConfig, uncertain bool) {
	t.Helper()
	for _, phase := range []string{"archive_retained", "contents_captured", "database_prepared", "import_executed"} {
		if phase == "import_executed" {
			prepared, err := v.f.db.x.f.srv.openProjectEnvironmentClonePostgresDatabasePreparation(t.Context(), v.f.db.x.f.lease, v.f.db.x.source, v.f.db.exports, v.f.sourceOID)
			if err != nil {
				t.Fatal(err)
			}
			v.f.target, err = prepared.receipt.TargetForWorker()
			if err != nil {
				t.Fatal(err)
			}
			if uncertain {
				v.f.childPostError = managedpostgres.ErrUnavailable
			}
		}
		progress, err := runCloneDataWorker(t, v, cfg)
		if phase == "import_executed" && uncertain {
			if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(progress, clonePostgresDataWorkProgress{}) {
				t.Fatal("uncertain import became progress", err)
			}
		} else if err != nil || progress.Phase != phase || progress.Verification.VerificationID != "" {
			t.Fatal("ordered durable data step", phase, progress.Phase, err)
		}
		v.handoff(t)
	}
	v.f.childPostError = nil
	if !v.f.restored || v.f.childCalls != 1 {
		t.Fatal("real archive was not restored once")
	}
	v.installVerificationReadConnector(t)
}

func TestPGClonePostgresDataWorkerDrivesRealArchiveContentsImportAndVerification(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "executed", true: "uncertain"}[uncertain], func(t *testing.T) {
			v, cfg := cloneDataWorkerFixture(t)
			prepareCloneDataWorkerThroughImport(t, v, cfg, uncertain)
			x := v.f.db.x
			sourceReads, artifactGets := x.p.readerSelectedSQLCalls, v.f.backend.gets
			// Original archive/manifest already exist. Recovery does not need a
			// producer, storage driver, or import tool configuration.
			progress, err := runCloneDataWorker(t, v, clonePostgresDataWorkConfig{Read: cfg.Read})
			if err != nil || progress.Phase != "contents_verified" || progress.Verification.State != "verified" || v.dataReads != 1 {
				t.Fatal("actual contents verification", err)
			}
			v.handoff(t)
			replay, err := runCloneDataWorker(t, v, clonePostgresDataWorkConfig{})
			if err != nil || !reflect.DeepEqual(replay, progress) || v.dataReads != 1 || v.f.childCalls != 1 || x.p.readerSelectedSQLCalls != sourceReads || v.f.backend.gets != artifactGets {
				t.Fatal("pipeline replay repeated data work", err)
			}
			v.assertClosed(t, true)
			if v.owner(t).State != "verified" || x.f.lease.Operation.Status != state.CloneOperationCapturing {
				t.Fatal("subordinate data proof advanced whole stage")
			}
		})
	}
}

func TestPGClonePostgresDataWorkerLostMatchRecoversFirstProof(t *testing.T) {
	v, cfg := cloneDataWorkerFixture(t)
	prepareCloneDataWorkerThroughImport(t, v, cfg, false)
	v.store.loseMatch = true
	if progress, err := runCloneDataWorker(t, v, cfg); err == nil || !reflect.DeepEqual(progress, clonePostgresDataWorkProgress{}) {
		t.Fatal("lost committed match leaked progress", err)
	}
	first := v.owner(t)
	if first.State != "compared" || v.dataReads != 1 {
		t.Fatal("match not committed")
	}
	v.handoff(t)
	progress, err := runCloneDataWorker(t, v, clonePostgresDataWorkConfig{})
	if err != nil || progress.Verification.State != "verified" || progress.Verification.Sealed.CiphertextSHA256 != first.Sealed.CiphertextSHA256 || v.dataReads != 1 || v.f.childCalls != 1 {
		t.Fatal("first match not recovered close-only", err)
	}
	v.assertClosed(t, true)
}

func TestPGClonePostgresDataWorkerImportClosurePostcheckPrecedesRead(t *testing.T) {
	v, cfg := cloneDataWorkerFixture(t)
	prepareCloneDataWorkerThroughImport(t, v, cfg, true)
	v.f.bootstrapPostError = managedpostgres.ErrUnavailable
	if progress, err := runCloneDataWorker(t, v, cfg); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(progress, clonePostgresDataWorkProgress{}) || v.dataReads != 0 {
		t.Fatal("failed import closure postcheck authorized verification", err)
	}
	v.f.bootstrapPostError = nil
	if progress, err := runCloneDataWorker(t, v, cfg); err != nil || progress.Verification.State != "verified" || v.dataReads != 1 {
		t.Fatal("closure recovery did not resume", err)
	}
	v.assertClosed(t, true)
}

func TestPGClonePostgresDataWorkerCloseOnlyRecoveryHasNoExecutionReceipt(t *testing.T) {
	v, cfg := cloneDataWorkerFixture(t)
	prepareCloneDataWorkerThroughImport(t, v, cfg, false)
	f, x := v.f, v.f.db.x
	// A concrete original import is already executed. A close-only recovery
	// returns no command receipt and never consults a configured storage driver.
	got, err := x.f.srv.projectEnvironmentClonePostgresImportWork(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, f.target,
		clonePostgresArchiveStorage{ID: f.artifact.ID, Fingerprint: f.artifact.Fingerprint}, "", "", 0, true)
	if err != nil || !reflect.DeepEqual(got, copyarchive.RestoreExecution{}) || f.childCalls != 1 {
		t.Fatal("close-only recovery synthesized execution", err)
	}
	stale := x.f.lease
	v.handoff(t)
	if _, err := x.f.srv.projectEnvironmentClonePostgresImportWork(context.Background(), stale, x.source, f.db.exports, f.sourceOID, f.target, cfg.Artifact, "", "", 0, true); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease closed import", err)
	}
}

func TestPGClonePostgresDataWorkerCloseOnlyCannotFundUndispatchedImport(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "reserved"}[reserved], func(t *testing.T) {
			v, cfg := cloneDataWorkerFixture(t)
			f, x := v.f, v.f.db.x
			for _, phase := range []string{"archive_retained", "contents_captured", "database_prepared"} {
				if progress, err := runCloneDataWorker(t, v, cfg); err != nil || progress.Phase != phase {
					t.Fatal("original preparation", err)
				}
			}
			prepared, err := x.f.srv.openProjectEnvironmentClonePostgresDatabasePreparation(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID)
			if err != nil {
				t.Fatal(err)
			}
			f.target, err = prepared.receipt.TargetForWorker()
			if err != nil {
				t.Fatal(err)
			}
			var original state.ProjectEnvironmentClonePostgresImport
			want := state.ErrNotFound
			if reserved {
				a, err := v.store.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
				if err != nil {
					t.Fatal(err)
				}
				original, _, err = v.store.ReserveProjectEnvironmentClonePostgresImport(t.Context(), x.f.lease, state.ProjectEnvironmentClonePostgresImportRequest{
					Input: a.Receipt, Target: f.target, DatabaseSQLPinsCiphertextSHA256: prepared.owner.Sealed.CiphertextSHA256,
					DatabasePlanCiphertextSHA256: prepared.owner.DatabasePlanCiphertextSHA256, ArchiveReservationSHA256: prepared.owner.ArchiveReservationSHA256})
				if err != nil {
					t.Fatal(err)
				}
				want = managedpostgres.ErrConflict
			}
			calls := x.p.targetSQLCalls
			if got, err := x.f.srv.projectEnvironmentClonePostgresImportWork(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, f.target, cfg.Artifact, "", "", 0, true); !errors.Is(err, want) || !reflect.DeepEqual(got, copyarchive.RestoreExecution{}) || x.p.targetSQLCalls != calls || f.childCalls != 0 {
				t.Fatal("close-only created/dispatched import", err)
			}
			owner, err := v.store.ProjectEnvironmentClonePostgresImportForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
			if reserved && (err != nil || !reflect.DeepEqual(owner, original)) || !reserved && !errors.Is(err, state.ErrNotFound) {
				t.Fatal("close-only changed intent", err)
			}
		})
	}
}
