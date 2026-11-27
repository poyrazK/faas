package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/state"
)

func attachPostgresRestoreReceipt(job *api.ManagedPostgresExport) {
	if job.Manifest == nil {
		return
	}
	recipient := cloneSealRecipient()
	if recipient == nil {
		return
	}
	for _, identity := range accountIdentities() {
		if identity.Recipient().String() == recipient.String() {
			job.Manifest.RestoreReceipt = job.Manifest.ArchiveManifest().Receipt(identity.String())
			return
		}
	}
}

func validPostgresRestoreReceipt(manifest api.ManagedPostgresExportManifest) bool {
	archive := manifest.ArchiveManifest()
	for _, identity := range accountIdentities() {
		if archive.VerifyReceipt(manifest.RestoreReceipt, identity.String()) {
			return true
		}
	}
	return false
}

func (s *server) uploadManagedPostgresArchive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pgarchive.UploadTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	c := http.NewResponseController(w)
	_ = c.SetReadDeadline(deadline)
	_ = c.SetWriteDeadline(deadline)
	r.Body = http.MaxBytesReader(w, r.Body, pgexport.MaxBytes+3*pgarchive.ManifestLimit)
	manifest, part, err := parseArchiveUpload(r)
	if err != nil || !validPostgresRestoreReceipt(manifest) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_postgres_restore_receipt", "Invalid PostgreSQL restore manifest",
			"Provide the unmodified manifest with the restore receipt produced by Gregale export."))
		return
	}
	id, operation := r.PathValue("id"), r.PathValue("operation_id")
	progress, err := s.managedPostgres.ImportArchive(ctx, acct.ID, id, operation, manifest.ArchiveManifest(), part)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(ctx, "managed_postgres.archive_import.requested", &acct.ID, map[string]any{"database_id": id, "operation_id": operation})
	code := http.StatusOK
	if progress.State == string(managedpostgres.ImportRunning) {
		code = http.StatusAccepted
	}
	writeJSON(w, code, progress)
}

func parseArchiveUpload(r *http.Request) (api.ManagedPostgresExportManifest, io.Reader, error) {
	var manifest api.ManagedPostgresExportManifest
	parts, err := r.MultipartReader()
	if err != nil {
		return manifest, nil, pgarchive.ErrArchive
	}
	meta, err := parts.NextPart()
	if err != nil || meta.FormName() != "manifest" {
		return manifest, nil, pgarchive.ErrArchive
	}
	raw, err := io.ReadAll(io.LimitReader(meta, pgarchive.ManifestLimit+1))
	if err != nil || len(raw) > pgarchive.ManifestLimit || json.Unmarshal(raw, &manifest) != nil {
		return manifest, nil, pgarchive.ErrArchive
	}
	part, err := parts.NextPart()
	if err != nil || part.FormName() != "archive" {
		return manifest, nil, pgarchive.ErrArchive
	}
	return manifest, &archiveUploadBody{part: part, remainingParts: parts}, nil
}

type archiveUploadBody struct {
	part           io.Reader
	remainingParts *multipart.Reader
}

func (r *archiveUploadBody) Read(p []byte) (int, error) {
	n, err := r.part.Read(p)
	if err == io.EOF && r.remainingParts != nil {
		_, tailErr := r.remainingParts.NextPart()
		r.remainingParts = nil
		if tailErr != io.EOF {
			return n, pgarchive.ErrArchive
		}
	}
	return n, err
}
