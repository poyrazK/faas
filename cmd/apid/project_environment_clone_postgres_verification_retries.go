package main

import (
	"context"
	"errors"
	"reflect"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private owned retry worker: at most one new comparison per invocation, with
// bounded durable admission. This does not publish complete stage readiness or
// supply the coordinator's production CPU/spool/billing admission and cleanup.
func (s *server) projectEnvironmentClonePostgresVerificationWithRetries(ctx context.Context, l state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32, cfg copycontents.Config) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	var zero state.ProjectEnvironmentClonePostgresVerificationAttempt
	verifications, ok := s.store.(state.ProjectEnvironmentClonePostgresVerificationStore)
	attempts, attemptOK := s.store.(state.ProjectEnvironmentClonePostgresVerificationAttemptStore)
	if !ok || !attemptOK {
		return zero, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	original, err := verifications.ProjectEnvironmentClonePostgresVerificationForLease(ctx, l, source.source.ID, oid)
	var history []state.ProjectEnvironmentClonePostgresVerificationAttempt
	if err == nil {
		history, err = attempts.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, l, source.source.ID, oid)
	}
	if errors.Is(err, state.ErrNotFound) || err == nil && len(history) == 0 {
		first, err := s.projectEnvironmentClonePostgresVerification(ctx, l, source, exports, oid, cfg)
		if err != nil {
			return zero, errors.Join(err, s.recoverProjectEnvironmentClonePostgresVerificationFailure(ctx, l, source, exports, oid, ""))
		}
		return state.ProjectEnvironmentClonePostgresVerificationAttempt{ProjectEnvironmentClonePostgresVerification: first, Attempt: 1, WindowOpenedAt: first.Sealed.OpenedAt, TargetDatabaseOID: first.Sealed.TargetDatabaseOID}, nil
	}
	if err != nil {
		return zero, err
	}
	w, err := s.openProjectEnvironmentClonePostgresVerificationRetry(ctx, l, source, exports, oid, original, history)
	if err != nil {
		return zero, err
	}
	head := w.head()
	if head.State == "failed" {
		if w.budget.OriginalVerificationID == "" {
			return zero, managedpostgres.ErrConflict
		}
		if setSecretRecipient == nil {
			return zero, managedpostgres.ErrUnavailable
		}
		recipient := setSecretRecipient()
		if !cloneInventoryCanOpen(recipient, w.identities) {
			return zero, managedpostgres.ErrUnavailable
		}
		next, _, err := attempts.ReserveProjectEnvironmentClonePostgresVerificationRetry(ctx, l, state.ProjectEnvironmentClonePostgresVerificationRetryRequest{
			ProjectEnvironmentClonePostgresVerificationRequest: state.ProjectEnvironmentClonePostgresVerificationRequest{Scope: original.Scope, DatabaseOID: oid, ContentsCiphertextSHA256: original.ContentsCiphertextSHA256,
				DatabaseSQLPinsCiphertextSHA256: original.DatabaseSQLPinsCiphertextSHA256, ImportID: original.ImportID, TargetFingerprint: original.TargetFingerprint, KeyID: recipient.String(), ReservedBytes: api.PostgresCopyVerificationCiphertextMaxBytes}, PreviousVerificationID: head.VerificationID})
		if err != nil {
			return zero, err
		}
		history, err = attempts.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, l, source.source.ID, oid)
		if err != nil {
			return zero, err
		}
		if len(history) != int(next.Attempt) || !reflect.DeepEqual(history[len(history)-1], next) {
			return zero, managedpostgres.ErrConflict
		}
		w, err = s.openProjectEnvironmentClonePostgresVerificationRetry(ctx, l, source, exports, oid, original, history)
		if err != nil {
			return zero, err
		}
		head = w.head()
	}
	if head.Attempt < 2 {
		return zero, managedpostgres.ErrConflict
	}
	if head.State == "reserved" {
		claimed, _, err := attempts.ClaimProjectEnvironmentClonePostgresVerificationAttempt(ctx, l, source.source.ID, oid, head.VerificationID)
		if err != nil {
			return zero, err
		}
		w.history[len(w.history)-1] = claimed
		head = claimed
	}
	result, err := w.run(ctx, cfg)
	if err != nil {
		return zero, errors.Join(err, s.recoverProjectEnvironmentClonePostgresVerificationFailure(ctx, l, source, exports, oid, head.VerificationID))
	}
	return result, nil
}

type clonePostgresVerificationRetryWorker struct {
	server        *server
	lease         state.ProjectEnvironmentCloneLease
	source        capturedProjectEnvironmentDatabasePlan
	exports       copyinventory.ExportPlan
	oid           uint32
	original      state.ProjectEnvironmentClonePostgresVerification
	history       []state.ProjectEnvironmentClonePostgresVerificationAttempt
	prepared      clonePostgresDatabasePreparation
	actual        copyarchive.RestoreTarget
	manifest      copycontents.Manifest
	identities    []*age.X25519Identity
	recipient     *age.X25519Recipient
	attempts      state.ProjectEnvironmentClonePostgresVerificationAttemptStore
	verifications state.ProjectEnvironmentClonePostgresVerificationStore
	pins          state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore
	budgets       state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore
	budget        state.ProjectEnvironmentClonePostgresVerificationReadBudget
	readConfig    *copycontents.Config
}

func (w *clonePostgresVerificationRetryWorker) head() state.ProjectEnvironmentClonePostgresVerificationAttempt {
	if len(w.history) > 0 {
		return w.history[len(w.history)-1]
	}
	return state.ProjectEnvironmentClonePostgresVerificationAttempt{ProjectEnvironmentClonePostgresVerification: w.original, Attempt: 1}
}
func (s *server) openProjectEnvironmentClonePostgresVerificationRetry(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32,
	original state.ProjectEnvironmentClonePostgresVerification, history []state.ProjectEnvironmentClonePostgresVerificationAttempt) (*clonePostgresVerificationRetryWorker, error) {
	if s.managedPostgres == nil || mfaIdentities == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	w := &clonePostgresVerificationRetryWorker{server: s, lease: l, source: source, exports: exports, oid: oid, original: original, history: history, identities: mfaIdentities()}
	var ok bool
	w.attempts, ok = s.store.(state.ProjectEnvironmentClonePostgresVerificationAttemptStore)
	if !ok {
		return nil, managedpostgres.ErrUnavailable
	}
	w.verifications, ok = s.store.(state.ProjectEnvironmentClonePostgresVerificationStore)
	if !ok {
		return nil, managedpostgres.ErrUnavailable
	}
	w.pins, ok = s.store.(state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore)
	if !ok {
		return nil, managedpostgres.ErrUnavailable
	}
	w.budgets, ok = s.store.(state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore)
	if !ok {
		return nil, managedpostgres.ErrUnavailable
	}
	var err error
	w.manifest, err = s.projectEnvironmentClonePostgresContents(ctx, l, exports, oid, 0, state.ProjectEnvironmentClonePostgresContentsLimits{}, nil)
	if err != nil {
		return nil, err
	}
	w.prepared, err = s.openProjectEnvironmentClonePostgresDatabasePreparation(ctx, l, source, exports, oid)
	if err != nil {
		return nil, err
	}
	w.actual, err = w.prepared.receipt.TargetForWorker()
	if err != nil {
		return nil, err
	}
	fp, err := w.actual.Fingerprint()
	if err != nil || !clonePostgresInventoryMatchesSource(l, source, w.actual.Scope) || !original.Scope.Equal(w.actual.Scope) || original.DatabaseOID != oid || original.TargetFingerprint != fp ||
		original.ManifestFingerprint != w.manifest.Fingerprint() || original.DatabaseSQLPinsCiphertextSHA256 != w.prepared.owner.Sealed.CiphertextSHA256 {
		return nil, managedpostgres.ErrConflict
	}
	for _, identity := range w.identities {
		if identity != nil && identity.Recipient().String() == w.head().KeyID {
			w.recipient = identity.Recipient()
			break
		}
	}
	if w.recipient == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	w.budget, err = clonePostgresVerificationReadBudget(ctx, w.budgets, l, original, copycontents.Config{}, false)
	if err != nil {
		return nil, err
	}
	if err = w.authorize(ctx, w.prepared.bootstrap); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *clonePostgresVerificationRetryWorker) authorize(ctx context.Context, target copyarchive.RestoreTarget) error {
	original, err := w.verifications.ProjectEnvironmentClonePostgresVerificationForLease(ctx, w.lease, w.source.source.ID, w.oid)
	if err != nil {
		return err
	}
	history, err := w.attempts.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, w.lease, w.source.source.ID, w.oid)
	if err != nil {
		return err
	}
	child, err := w.pins.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx, w.lease, w.source.source.ID, w.oid)
	if err == nil && (target != w.prepared.bootstrap || !reflect.DeepEqual(original, w.original) || !reflect.DeepEqual(history, w.history) || !sameClonePostgresDatabaseSQLPins(child, w.prepared.owner)) {
		err = managedpostgres.ErrConflict
	}
	if err == nil {
		err = authorizeClonePostgresVerificationReadBudget(ctx, w.budgets, w.lease, w.original, w.budget)
	}
	if err == nil && w.readConfig != nil {
		err = w.server.authorizeProjectEnvironmentClonePostgresRead(ctx, *w.readConfig)
	}
	return err
}
func (w *clonePostgresVerificationRetryWorker) bootstrap(ctx context.Context, run func(context.Context, *pgx.Conn) error) error {
	request, err := w.server.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, w.lease, w.source, w.actual.Scope, w.prepared.bootstrap)
	if err != nil {
		return err
	}
	return w.server.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(w.source), request, func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
		return run(ctx, conn)
	})
}
func (w *clonePostgresVerificationRetryWorker) close(ctx context.Context, a state.ProjectEnvironmentClonePostgresVerificationAttempt) (copydatabases.VerificationClosure, error) {
	var closure copydatabases.VerificationClosure
	var nativeErr error
	err := w.bootstrap(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		if a.Attempt == 1 {
			closure, nativeErr = w.prepared.receipt.CloseVerificationAccess(ctx, conn, w.exports, uuid.MustParse(a.ImportID), uuid.MustParse(a.VerificationID), w.authorize)
		} else {
			closure, nativeErr = w.prepared.receipt.CloseVerificationRetryAccess(ctx, conn, w.exports, uuid.MustParse(a.ImportID), uuid.MustParse(a.VerificationID), uuid.MustParse(a.PreviousVerificationID), w.authorize)
		}
		// Absence must survive complete provider postchecks. Returning NotFound
		// from the callback would give it precedence over the provider's error.
		if nativeErr == managedpostgres.ErrNotFound {
			return nil
		}
		return nativeErr
	})
	if err != nil {
		return copydatabases.VerificationClosure{}, err
	}
	if nativeErr != nil {
		return copydatabases.VerificationClosure{}, nativeErr
	}
	return closure, nil
}

func (w *clonePostgresVerificationRetryWorker) recordFailure(ctx context.Context, closure copydatabases.VerificationClosure) error {
	if err := w.authorize(ctx, w.prepared.bootstrap); err != nil {
		return err
	}
	a, err := w.attempts.RecordProjectEnvironmentClonePostgresVerificationFailure(ctx, w.lease, w.source.source.ID, w.oid, w.head().VerificationID,
		state.ProjectEnvironmentClonePostgresVerificationFailure{Target: w.actual, Preparation: w.prepared.receipt, Closure: closure})
	if err == nil {
		if len(w.history) == 0 {
			w.history = append(w.history, a)
		} else {
			w.history[len(w.history)-1] = a
		}
	}
	return err
}

// Re-read durable evidence before classifying an error. A lost committed match
// is compared, so it is never changed into failed or redispatched as a new read.
func (s *server) recoverProjectEnvironmentClonePostgresVerificationFailure(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32, selected string) error {
	v, ok := s.store.(state.ProjectEnvironmentClonePostgresVerificationStore)
	a, attemptOK := s.store.(state.ProjectEnvironmentClonePostgresVerificationAttemptStore)
	if !ok || !attemptOK {
		return managedpostgres.ErrUnavailable
	}
	original, err := v.ProjectEnvironmentClonePostgresVerificationForLease(ctx, l, source.source.ID, oid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	history, err := a.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, l, source.source.ID, oid)
	if err != nil {
		return err
	}
	head := state.ProjectEnvironmentClonePostgresVerificationAttempt{ProjectEnvironmentClonePostgresVerification: original, Attempt: 1}
	if len(history) > 0 {
		head = history[len(history)-1]
	}
	if head.State != "verifying" || selected != "" && head.VerificationID != selected || selected == "" && head.Attempt != 1 {
		return nil
	}
	w, err := s.openProjectEnvironmentClonePostgresVerificationRetry(ctx, l, source, exports, oid, original, history)
	if err != nil {
		return err
	}
	closure, err := w.close(ctx, head)
	// An absent retry row is undispatched intent. An original missing row returns
	// conflict; its callback API independently authenticates any first dispatch.
	if errors.Is(err, managedpostgres.ErrNotFound) || errors.Is(err, managedpostgres.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	return w.recordFailure(ctx, closure)
}
func (w *clonePostgresVerificationRetryWorker) run(ctx context.Context, cfg copycontents.Config) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	var zero state.ProjectEnvironmentClonePostgresVerificationAttempt
	var releaseRead func()
	defer func() {
		if releaseRead != nil {
			releaseRead()
		}
		w.readConfig = nil
	}()
	a := w.head()
	owner, imported := uuid.MustParse(a.VerificationID), uuid.MustParse(a.ImportID)
	var retained copycontents.RetainedMatch
	var closure copydatabases.VerificationClosure
	var err error
	if a.State == "compared" || a.State == "verified" {
		retained, err = copycontents.OpenMatch(w.identities, w.manifest, w.actual, owner, imported, a.Sealed)
		if err != nil {
			return zero, err
		}
		closure, err = w.close(ctx, a)
	} else if a.State == "verifying" {
		closure, err = w.close(ctx, a)
		if err == nil {
			if err = w.recordFailure(ctx, closure); err != nil {
				return zero, err
			}
			return zero, managedpostgres.ErrConflict
		}
		if !errors.Is(err, managedpostgres.ErrNotFound) {
			return zero, err
		}
		prior := w.history[len(w.history)-2]
		var predecessor copydatabases.VerificationClosure
		predecessor, err = w.close(ctx, prior)
		if err != nil {
			return zero, err
		}
		if predecessor.AttemptForWorker() != prior.Attempt || !predecessor.OpenedAt().Equal(prior.WindowOpenedAt) || !predecessor.ClosedAt().Equal(prior.NativeClosedAt) ||
			!predecessor.MatchesForWorker(w.prepared.receipt, imported, uuid.MustParse(prior.VerificationID), prior.WindowOpenedAt) {
			return zero, managedpostgres.ErrConflict
		}
		var childRequest managedpostgres.SnapshotCopyTargetDatabaseSQLRequest
		childRequest, err = w.server.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, w.lease, w.source, w.actual.Scope, w.actual)
		if err != nil {
			return zero, err
		}
		err = w.bootstrap(ctx, func(ctx context.Context, conn *pgx.Conn) error {
			var err error
			closure, err = w.prepared.receipt.WithVerificationRetryAccessAdmitted(ctx, conn, w.exports, imported, owner, predecessor, w.authorize,
				func(ctx context.Context, target copyarchive.RestoreTarget) error {
					if err := w.authorize(ctx, target); err != nil {
						return err
					}
					var err error
					cfg, releaseRead, err = w.server.admitClonePostgresVerificationRead(ctx, w.budgets, w.lease, w.original, a.VerificationID, a.Attempt, &w.budget, cfg)
					if err == nil {
						w.readConfig = &cfg
					}
					return err
				},
				func(ctx context.Context, access copydatabases.VerificationTarget) error {
					var err error
					retained, err = w.server.projectEnvironmentClonePostgresCompare(ctx, w.source, w.prepared.bootstrap, childRequest, w.manifest, w.actual, owner, imported, a.Attempt, w.recipient, w.identities, cfg, w.authorize, access,
						func(ctx context.Context, sealed copycontents.SealedMatch) (copycontents.SealedMatch, error) {
							recorded, err := w.attempts.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx, w.lease, w.source.source.ID, w.oid, a.VerificationID, sealed)
							if err == nil {
								w.history[len(w.history)-1] = recorded
							}
							return recorded.Sealed, err
						})
					return err
				})
			return err
		})
	} else {
		return zero, managedpostgres.ErrConflict
	}
	if err != nil {
		return zero, err
	}
	if err = w.authorize(ctx, w.prepared.bootstrap); err != nil {
		return zero, err
	}
	result, err := w.attempts.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(ctx, w.lease, w.source.source.ID, w.oid, a.VerificationID,
		state.ProjectEnvironmentClonePostgresVerificationCompletion{Manifest: w.manifest, Match: retained, Target: w.actual, Preparation: w.prepared.receipt, Closure: closure})
	if err != nil {
		return zero, err
	}
	return result, nil
}
