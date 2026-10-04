//go:build !no_pg

// adr:566
package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compose the actual native protocol with the durable attempt store. This
// qualifies storage admission, not automatic worker/coordinator retry wiring.
func verificationAttemptBootstrap(t *testing.T, v *verificationWorkerFixture, run func(context.Context, *pgx.Conn, clonePostgresDatabasePreparation) error) error {
	t.Helper()
	f, x := v.f, v.f.db.x
	s := x.f.srv
	prepared, err := s.openProjectEnvironmentClonePostgresDatabasePreparation(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID)
	if err != nil {
		return err
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(t.Context(), x.f.lease, x.source, f.target.Scope, prepared.bootstrap)
	if err != nil {
		return err
	}
	return s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(t.Context(), clonePostgresSnapshotDefinition(x.source), request, func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
		return run(ctx, conn, prepared)
	})
}

func verificationAttemptFirstFailure(t *testing.T, v *verificationWorkerFixture) (state.ProjectEnvironmentClonePostgresVerificationAttempt, copydatabases.VerificationClosure, state.ProjectEnvironmentClonePostgresVerificationRetryRequest) {
	t.Helper()
	f, x := v.f, v.f.db.x
	v.importData(t, false)
	f.childPostError = managedpostgres.ErrUnavailable
	if got, err := v.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || got.VerificationID != "" {
		t.Fatal("real first comparison did not fail after child provider postcheck", err)
	}
	f.childPostError = nil
	original := v.owner(t)
	v.assertClosed(t, true)
	owner := uuid.MustParse(original.VerificationID)
	imported := uuid.MustParse(original.ImportID)
	authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
		fresh, err := v.store.ProjectEnvironmentClonePostgresVerificationForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err == nil && (target != x.target || !reflect.DeepEqual(fresh, original)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	var closure copydatabases.VerificationClosure
	var proof state.ProjectEnvironmentClonePostgresVerificationFailure
	if err := verificationAttemptBootstrap(t, v, func(ctx context.Context, conn *pgx.Conn, p clonePostgresDatabasePreparation) error {
		var err error
		closure, err = p.receipt.CloseVerificationAccess(ctx, conn, f.db.exports, imported, owner, authorize)
		proof = state.ProjectEnvironmentClonePostgresVerificationFailure{Target: f.target, Preparation: p.receipt, Closure: closure}
		return err
	}); err != nil {
		t.Fatal("actual closed original window", err)
	}
	zero := proof
	zero.Closure = copydatabases.VerificationClosure{}
	if _, err := v.store.RecordProjectEnvironmentClonePostgresVerificationFailure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, original.VerificationID, zero); !errors.Is(err, state.ErrConflict) {
		t.Fatal("synthetic closure authorized retry", err)
	}
	failed, err := v.store.RecordProjectEnvironmentClonePostgresVerificationFailure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, original.VerificationID, proof)
	if err != nil || failed.State != "failed" || failed.Attempt != 1 || !failed.WindowOpenedAt.Equal(closure.OpenedAt()) || !failed.NativeClosedAt.Equal(closure.ClosedAt()) {
		t.Fatal("actual failure not retained", err)
	}
	replay, err := v.store.RecordProjectEnvironmentClonePostgresVerificationFailure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, original.VerificationID, proof)
	if err != nil || !reflect.DeepEqual(replay, failed) {
		t.Fatal("lost failure response replaced evidence", err)
	}
	if _, err := v.run(t); !errors.Is(err, managedpostgres.ErrConflict) || v.dataReads != 1 {
		t.Fatal("original worker reopened failed owner", err)
	}
	return failed, closure, state.ProjectEnvironmentClonePostgresVerificationRetryRequest{ProjectEnvironmentClonePostgresVerificationRequest: state.ProjectEnvironmentClonePostgresVerificationRequest{
		Scope: original.Scope, DatabaseOID: original.DatabaseOID, ContentsCiphertextSHA256: original.ContentsCiphertextSHA256, DatabaseSQLPinsCiphertextSHA256: original.DatabaseSQLPinsCiphertextSHA256, ImportID: original.ImportID, TargetFingerprint: original.TargetFingerprint, KeyID: original.KeyID, ReservedBytes: original.ReservedBytes}, PreviousVerificationID: original.VerificationID}
}
func verificationAttemptAuthorize(v *verificationWorkerFixture, held *state.ProjectEnvironmentClonePostgresVerificationAttempt) func(context.Context, copyarchive.RestoreTarget) error {
	return func(ctx context.Context, target copyarchive.RestoreTarget) error {
		f, x := v.f, v.f.db.x
		history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err == nil && (target != x.target || len(history) != int(held.Attempt) || !reflect.DeepEqual(history[len(history)-1], *held)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
}
func reserveClaimVerificationAttempt(t *testing.T, v *verificationWorkerFixture, r state.ProjectEnvironmentClonePostgresVerificationRetryRequest) state.ProjectEnvironmentClonePostgresVerificationAttempt {
	t.Helper()
	x := v.f.db.x
	a, first, err := v.store.ReserveProjectEnvironmentClonePostgresVerificationRetry(t.Context(), x.f.lease, r)
	if err != nil || !first || a.State != "reserved" {
		t.Fatal("retry intent", err)
	}
	replay, first, err := v.store.ReserveProjectEnvironmentClonePostgresVerificationRetry(t.Context(), x.f.lease, r)
	if err != nil || first || !reflect.DeepEqual(a, replay) {
		t.Fatal("lost reserve reply changed owner", err)
	}
	a, first, err = v.store.ClaimProjectEnvironmentClonePostgresVerificationAttempt(t.Context(), x.f.lease, r.Scope.SourceDatabaseID, r.DatabaseOID, a.VerificationID)
	if err != nil || !first || a.State != "verifying" {
		t.Fatal("retry dispatch", err)
	}
	replay, first, err = v.store.ClaimProjectEnvironmentClonePostgresVerificationAttempt(t.Context(), x.f.lease, r.Scope.SourceDatabaseID, r.DatabaseOID, a.VerificationID)
	if err != nil || first || !reflect.DeepEqual(a, replay) {
		t.Fatal("lost claim reply changed dispatch", err)
	}
	if _, _, err := v.store.AllocateProjectEnvironmentClonePostgresVerificationRead(t.Context(), x.f.lease, state.ProjectEnvironmentClonePostgresVerificationReadRequest{
		SourceDatabaseID: r.Scope.SourceDatabaseID, DatabaseOID: r.DatabaseOID, VerificationID: a.VerificationID, Attempt: a.Attempt, ReadBytes: v.cfg.MaxBytes, SortMemoryBytes: int64(v.cfg.SortMemoryBytes), SortDiskBytes: v.cfg.SortDiskBytes}); err != nil {
		t.Fatal("manual native composition read debit", err)
	}
	return a
}

func TestPGClonePostgresVerificationAttemptsRealClosedFailuresBoundNewOwners(t *testing.T) {
	v := cloneVerificationWorkerFixture(t)
	f, x := v.f, v.f.db.x
	original, previous, r := verificationAttemptFirstFailure(t, v)
	for attempt := int32(2); attempt <= api.PostgresCopyVerificationAttemptsMax; attempt++ {
		a := reserveClaimVerificationAttempt(t, v, r)
		authorize := verificationAttemptAuthorize(v, &a)
		owner, imported := uuid.MustParse(a.VerificationID), uuid.MustParse(a.ImportID)
		if err := verificationAttemptBootstrap(t, v, func(ctx context.Context, conn *pgx.Conn, p clonePostgresDatabasePreparation) error {
			got, err := p.receipt.WithVerificationRetryAccess(ctx, conn, f.db.exports, imported, owner, previous, authorize, func(ctx context.Context, access copydatabases.VerificationTarget) error {
				identity, err := access.IdentityForWorker()
				if err != nil || identity.Attempt != attempt || identity.OwnerID != owner {
					return managedpostgres.ErrConflict
				}
				return managedpostgres.ErrUnavailable
			})
			if got != (copydatabases.VerificationClosure{}) {
				return errors.New("failed native window published closure")
			}
			return err
		}); !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatal("native retry failure", err)
		}
		var proof state.ProjectEnvironmentClonePostgresVerificationFailure
		if err := verificationAttemptBootstrap(t, v, func(ctx context.Context, conn *pgx.Conn, p clonePostgresDatabasePreparation) error {
			var err error
			previous, err = p.receipt.CloseVerificationRetryAccess(ctx, conn, f.db.exports, imported, owner, uuid.MustParse(a.PreviousVerificationID), authorize)
			proof = state.ProjectEnvironmentClonePostgresVerificationFailure{Target: f.target, Preparation: p.receipt, Closure: previous}
			return err
		}); err != nil {
			t.Fatal("close-only retry recovery", err)
		}
		failed, err := v.store.RecordProjectEnvironmentClonePostgresVerificationFailure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, proof)
		if err != nil || failed.Attempt != attempt || failed.State != "failed" {
			t.Fatal("opaque retry failure admission", err)
		}
		if _, err := v.store.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, copycontents.SealedMatch{}); err == nil {
			t.Fatal("failed owner acquired proof")
		}
		r.PreviousVerificationID = a.VerificationID
	}
	if _, _, err := v.store.ReserveProjectEnvironmentClonePostgresVerificationRetry(t.Context(), x.f.lease, r); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatal("fourth attempt admitted", err)
	}
	history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil || len(history) != 3 || !reflect.DeepEqual(history[0], original) {
		t.Fatal("first history replaced", err)
	}
	if v.dataReads != 1 || f.childCalls != 1 || v.owner(t).State != "verifying" {
		t.Fatal("retry redispatched import or original read")
	}
	v.assertClosed(t, true)
}

func TestPGClonePostgresVerificationAttemptsRealRetryMatchAndExactClosure(t *testing.T) {
	v := cloneVerificationWorkerFixture(t)
	f, x := v.f, v.f.db.x
	s := x.f.srv
	original, previous, r := verificationAttemptFirstFailure(t, v)
	a := reserveClaimVerificationAttempt(t, v, r)
	authorize := verificationAttemptAuthorize(v, &a)
	owner, imported := uuid.MustParse(a.VerificationID), uuid.MustParse(a.ImportID)
	manifest, err := s.projectEnvironmentClonePostgresContents(t.Context(), x.f.lease, f.db.exports, f.sourceOID, 0, state.ProjectEnvironmentClonePostgresContentsLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var closure copydatabases.VerificationClosure
	var completed state.ProjectEnvironmentClonePostgresVerificationCompletion
	readConfig := v.cfg
	var releaseRead func()
	defer func() {
		if releaseRead != nil {
			releaseRead()
		}
	}()
	if err = verificationAttemptBootstrap(t, v, func(ctx context.Context, conn *pgx.Conn, p clonePostgresDatabasePreparation) error {
		var err error
		closure, err = p.receipt.WithVerificationRetryAccessAdmitted(ctx, conn, f.db.exports, imported, owner, previous, authorize,
			func(ctx context.Context, target copyarchive.RestoreTarget) error {
				if err := authorize(ctx, target); err != nil {
					return err
				}
				var err error
				readConfig, releaseRead, err = s.reserveProjectEnvironmentClonePostgresRead(ctx, readConfig)
				return err
			}, func(ctx context.Context, access copydatabases.VerificationTarget) error {
				actual, e := access.TargetForWorker()
				identity, ie := access.IdentityForWorker()
				if e != nil || ie != nil || actual != f.target || identity.Attempt != a.Attempt || identity.OwnerID != owner || identity.ImportID != imported {
					return managedpostgres.ErrConflict
				}
				request, e := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, x.f.lease, x.source, f.target.Scope, f.target)
				if e != nil {
					return e
				}
				var matched copycontents.Match
				e = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(x.source), request, func(ctx context.Context, child *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
					placement := func(ctx context.Context, got *pgx.Conn, target copyarchive.RestoreTarget) error {
						if got != child || target != f.target {
							return managedpostgres.ErrConflict
						}
						return authorize(ctx, p.bootstrap)
					}
					return access.WithReadOnly(ctx, child, placement, func(ctx context.Context, tx pgx.Tx) error {
						var e error
						matched, e = manifest.CompareTarget(ctx, tx, f.target, readConfig, placement)
						return e
					})
				})
				if e != nil {
					return e
				}
				sealed, e := copycontents.SealMatch(setSecretRecipient(), manifest, f.target, owner, imported, identity.OpenedAt, matched)
				if e != nil {
					return e
				}
				a, e = v.store.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx, x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, sealed)
				if e != nil {
					return e
				}
				retained, e := copycontents.OpenMatch(mfaIdentities(), manifest, f.target, owner, imported, a.Sealed)
				completed = state.ProjectEnvironmentClonePostgresVerificationCompletion{Manifest: manifest, Match: retained, Target: f.target, Preparation: p.receipt}
				return e
			})
		return err
	}); err != nil {
		t.Fatal("actual retry comparison and provider postchecks", err)
	}
	completed.Closure = closure
	forged := completed
	forged.Closure = previous
	if _, err = v.store.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("predecessor closed current proof", err)
	}
	failure := state.ProjectEnvironmentClonePostgresVerificationFailure{Target: f.target, Preparation: completed.Preparation, Closure: closure}
	if _, err = v.store.RecordProjectEnvironmentClonePostgresVerificationFailure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, failure); !errors.Is(err, state.ErrConflict) {
		t.Fatal("actual compared closure granted failure", err)
	}
	verified, err := v.store.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, completed)
	if err != nil || verified.State != "verified" || !verified.NativeClosedAt.Equal(closure.ClosedAt()) {
		t.Fatal("real retained match did not close", err)
	}
	replay, err := v.store.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID, a.VerificationID, completed)
	if err != nil || !reflect.DeepEqual(replay, verified) {
		t.Fatal("lost closure response replaced first times", err)
	}
	r.PreviousVerificationID = a.VerificationID
	if _, _, err = v.store.ReserveProjectEnvironmentClonePostgresVerificationRetry(t.Context(), x.f.lease, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("verified owner retried", err)
	}
	history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil || len(history) != 2 || !reflect.DeepEqual(history[0], original) {
		t.Fatal("verified retry damaged original failure", err)
	}
	if v.dataReads != 2 || f.childCalls != 1 {
		t.Fatal("actual retry read count or import changed")
	}
	v.assertClosed(t, true)
}
