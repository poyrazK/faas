package apidserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) startManagedPostgresExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.postgresExports == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ManagedPostgresExportRequest
	if decodeJSONSized(r, &request, 1<<10) != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	job, err := s.postgresExports.Start(r.Context(), acct.ID, r.PathValue("id"), request.RequestID)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.export.requested", &acct.ID, map[string]any{"database_id": r.PathValue("id"), "export_id": job.ID})
	code := http.StatusAccepted
	if job.State == "ready" || job.State == "failed" {
		code = http.StatusOK
	}
	attachPostgresRestoreReceipt(&job)
	writeJSON(w, code, job)
}

func (s *server) getManagedPostgresExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.postgresExportOwner(w, r, acct)
	if !ok {
		return
	}
	job, err := s.postgresExports.Status(r.Context(), acct.ID, db.OrgID, db.ID, r.PathValue("export_id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	attachPostgresRestoreReceipt(&job)
	writeJSON(w, http.StatusOK, job)
}

func (s *server) downloadManagedPostgresExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.postgresExportOwner(w, r, acct)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_ = http.NewResponseController(w).SetWriteDeadline(deadline)
	id := r.PathValue("export_id")
	manifest, reader, err := s.postgresExports.Open(ctx, acct.ID, db.OrgID, db.ID, id)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	defer reader.Close() //nolint:errcheck
	s.audit.Emit(r.Context(), "managed_postgres.export.downloaded", &acct.ID, map[string]any{"database_id": db.ID, "export_id": id})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="postgres.dump"`)
	w.Header().Set("Content-Length", strconv.FormatInt(manifest.SizeBytes, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Content-SHA256", manifest.SHA256)
	if _, err := io.CopyN(w, reader, manifest.SizeBytes); err != nil {
		panic(http.ErrAbortHandler)
	}
	if _, err := io.CopyN(io.Discard, reader, 1); !errors.Is(err, io.EOF) {
		panic(http.ErrAbortHandler)
	}
}

func (s *server) postgresExportOwner(w http.ResponseWriter, r *http.Request, acct state.Account) (managedpostgres.Database, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.postgresExports == nil || s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return managedpostgres.Database{}, false
	}
	db, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return managedpostgres.Database{}, false
	}
	return db, true
}
