package apidserver

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/postgresexport"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type postgresBackupBuckets struct{ server *server }

func (b postgresBackupBuckets) load(ctx context.Context, dest postgresexport.BackupDestination) (state.ObjectBucket, error) {
	store, ok := b.server.store.(state.ObjectBucketStore)
	if !ok {
		return state.ObjectBucket{}, managedpostgres.ErrUnavailable
	}
	app, err := b.server.store.AppByID(ctx, dest.BucketAppID)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	if app.AccountID != dest.BucketAccountID || app.OrgID != dest.OrgID {
		return state.ObjectBucket{}, state.ErrNotFound
	}
	bucket, err := store.GetObjectBucket(ctx, app.AccountID, app.ID, dest.BucketID)
	if err == nil && bucket.OrgID != dest.OrgID {
		err = state.ErrNotFound
	}
	return bucket, err
}

func (b postgresBackupBuckets) CheckBackupDestination(ctx context.Context, dest postgresexport.BackupDestination) error {
	bucket, err := b.load(ctx, dest)
	if err != nil {
		return err
	}
	return b.server.object.CheckPrivateBackupBucket(bucket)
}

func (b postgresBackupBuckets) PutBackupObject(ctx context.Context, dest postgresexport.BackupDestination, key, mediaType, digest string, size int64, body io.Reader) error {
	bucket, err := b.load(ctx, dest)
	if err != nil {
		return err
	}
	return b.server.object.PutImmutableBackupObject(ctx, bucket, key, mediaType, digest, size, body)
}

func (s *server) managedPostgresBackupDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) (managedpostgres.Database, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.postgresBackupSchedules == nil {
		managedPostgresNotConfiguredProblem(w)
		return managedpostgres.Database{}, false
	}
	return s.postgresExportOwner(w, r, acct)
}

func (s *server) getManagedPostgresBackupSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.managedPostgresBackupDatabase(w, r, acct)
	if !ok {
		return
	}
	view, err := s.postgresBackupSchedules.Get(r.Context(), acct.ID, db.OrgID, db.ID)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) setManagedPostgresBackupSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.managedPostgresBackupDatabase(w, r, acct)
	if !ok {
		return
	}
	var request api.SetManagedPostgresBackupScheduleRequest
	bad := decodeJSONSized(r, &request, 1<<10)
	bucketID, err := uuid.Parse(request.BucketID)
	if bad != nil || !api.ValidAppSlug(request.BucketApp) || err != nil || bucketID == uuid.Nil || bucketID.String() != request.BucketID {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	dest, ok := s.authorizePostgresBackupBucket(w, r, acct, db.OrgID, request)
	if !ok {
		return
	}
	view, err := s.postgresBackupSchedules.Set(r.Context(), acct.ID, db.ID, request.DailyAtUTC, dest)
	if err != nil {
		if errors.Is(err, managedpostgres.ErrInvalid) || errors.Is(err, managedpostgres.ErrConflict) || errors.Is(err, managedpostgres.ErrNotFound) ||
			errors.Is(err, managedpostgres.ErrUnavailable) || errors.Is(err, managedpostgres.ErrUnsupported) {
			managedPostgresProblem(w, err)
		} else {
			object.BucketProblem(w, err)
		}
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.backup_schedule.set", &acct.ID, postgresaccess.AuditData(r.Context(), map[string]any{
		"database_id": db.ID, "bucket_id": view.BucketID, "daily_at_utc": view.DailyAtUTC,
	}))
	writeJSON(w, http.StatusOK, view)
}

func (s *server) authorizePostgresBackupBucket(w http.ResponseWriter, r *http.Request, acct state.Account, org string, request api.SetManagedPostgresBackupScheduleRequest) (postgresexport.BackupDestination, bool) {
	app, err := s.store.AppBySlug(r.Context(), request.BucketApp)
	if err != nil || app.OrgID != org || !postgresaccess.CanBind(r.Context(), app, acct) {
		object.BucketProblem(w, state.ErrNotFound)
		return postgresexport.BackupDestination{}, false
	}
	// Apply the same project-key and organization checks as a bucket write
	// capability while preserving the source database's request fence.
	capabilityRequest := r.Clone(r.Context())
	_, problem := appaccess.AuthorizeRoute(capabilityRequest, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), app,
		"POST /v1/apps/{slug}/buckets/{bucket}/signed-url")
	if problem != nil {
		api.WriteProblem(w, problem)
		return postgresexport.BackupDestination{}, false
	}
	dest := postgresexport.BackupDestination{OrgID: org, BucketAppID: app.ID, BucketAccountID: app.AccountID, BucketID: request.BucketID}
	bucket, err := (postgresBackupBuckets{s}).load(r.Context(), dest)
	if err != nil {
		object.BucketProblem(w, err)
		return postgresexport.BackupDestination{}, false
	}
	return dest, s.object.AuthorizeBucketData(w, r, bucket, state.ObjectBucketPermissionWrite)
}

func (s *server) deleteManagedPostgresBackupSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.managedPostgresBackupDatabase(w, r, acct)
	if !ok {
		return
	}
	if err := s.postgresBackupSchedules.Delete(r.Context(), acct.ID, db.OrgID, db.ID); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.backup_schedule.deleted", &acct.ID, postgresaccess.AuditData(r.Context(), map[string]any{"database_id": db.ID}))
	w.WriteHeader(http.StatusNoContent)
}
