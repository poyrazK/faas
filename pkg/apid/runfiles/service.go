// Package runfiles implements account-scoped immutable file transfer for Runs.
package runfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/runfilestore"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type Access struct {
	Account   state.Account
	Principal *string
	Wide      bool
}
type Service struct {
	GrantTokenHash func(string) ([]byte, bool)
	Store          state.ExecutionFileStore
	Storage        storage.StorageBackend
	Recipients     []*age.X25519Recipient
	Identities     []*age.X25519Identity
}

func UploadID(a Access, size int64, digest string) string {
	principal := "account"
	if a.Principal != nil {
		principal = *a.Principal
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("gregale:execution-file:v1:%s:%s:%d:%s", a.Account.ID, principal, size, digest))).String()
}
func Response(f state.ExecutionFile) api.ExecutionFileResponse {
	expiry := f.ExpiresAt
	return api.ExecutionFileResponse{ID: f.ID, Status: f.Status, SizeBytes: f.SizeBytes, SHA256: f.SHA256, ExpiresAt: &expiry, ContentURL: "/v1/execution-files/" + f.ID + "/content"}
}
func owns(a Access, f state.ExecutionFile) bool {
	return a.Wide || a.Principal == nil && f.PrincipalID == nil || a.Principal != nil && f.PrincipalID != nil && *a.Principal == *f.PrincipalID
}
func (s Service) Get(ctx context.Context, a Access, id string) (state.ExecutionFile, error) {
	if s.Store == nil || !runfile.ValidID(id) {
		return state.ExecutionFile{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run file not found", "no such Run file")
	}
	f, err := s.Store.ExecutionFileByID(ctx, a.Account.ID, id)
	if err != nil || !owns(a, f) {
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return state.ExecutionFile{}, api.ErrCapacity("Run file metadata is unavailable")
		}
		return state.ExecutionFile{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run file not found", "no such Run file")
	}
	return f, nil
}

// Prepare does not reserve quota. This allows an admission-key replay to find
// its original receipt even after a referenced upload has expired.
func (s Service) Prepare(ctx context.Context, a Access, r api.CreateExecutionFileRequest) (api.ExecutionFileResponse, error) {
	if r.SizeBytes < 0 || r.SizeBytes > api.ExecutionFileHardMaxBytes || !runfile.ValidDigest(r.SHA256) {
		return api.ExecutionFileResponse{}, api.ErrValidation("size_bytes and sha256 are outside Run file bounds")
	}
	id := UploadID(a, r.SizeBytes, r.SHA256)
	f, err := s.Get(ctx, a, id)
	var p *api.Problem
	if err != nil {
		if !errors.As(err, &p) || p.Status != http.StatusNotFound {
			return api.ExecutionFileResponse{}, err
		}
		return api.ExecutionFileResponse{ID: id, Status: "missing", SizeBytes: r.SizeBytes, SHA256: r.SHA256, UploadRequired: true, ContentURL: "/v1/execution-files/" + id + "/content"}, nil
	}
	out := Response(f)
	out.UploadRequired = f.Status != "ready" || !f.ExpiresAt.After(time.Now().UTC())
	if !f.ExpiresAt.After(time.Now().UTC()) {
		out.Status = "expired"
	}
	return out, nil
}
func (s Service) Upload(ctx context.Context, a Access, id string, size int64, digest string, body io.ReadCloser) (api.ExecutionFileResponse, error) {
	defer body.Close() //nolint:errcheck
	if s.Store == nil || s.Storage == nil {
		return api.ExecutionFileResponse{}, api.ErrCapacity("Run file storage is unavailable")
	}
	limits := a.Account.Plan.ExecutionFileLimits()
	if size > limits.MaxFileBytes {
		return api.ExecutionFileResponse{}, api.NewProblem(http.StatusRequestEntityTooLarge, "execution_file_too_large", "Run file exceeds plan limit", "size_bytes exceeds this plan's per-file limit").WithByteLimit(limits.MaxFileBytes, size).WithDocs(api.ExecutionFilesDocsURL)
	}
	if size < 0 || !runfile.ValidDigest(digest) || id != UploadID(a, size, digest) {
		return api.ExecutionFileResponse{}, api.ErrValidation("upload requires its prepared file ID, Content-Length, and X-Content-SHA256 within plan limits")
	}
	now := time.Now().UTC()
	token := uuid.NewString()
	lease := now.Add(api.ExecutionFileUploadTimeout)
	key := runfile.Key(a.Account.ID, id, token)
	private, sealed, err := runfilestore.NewKey(a.Account.ID, id, key, s.Recipients)
	if err != nil {
		return api.ExecutionFileResponse{}, api.ErrCapacity("Run file sealing is unavailable")
	}
	f, replayed, err := s.Store.ClaimExecutionFileUpload(ctx, state.ExecutionFile{ID: id, AccountID: a.Account.ID, PrincipalID: a.Principal, Kind: "upload", Status: "uploading", SizeBytes: size, SHA256: digest, StorageKey: key, SealedKey: sealed, Token: &token, LeaseExpiresAt: &lease, ExpiresAt: now.Add(api.ExecutionFileRetention), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return api.ExecutionFileResponse{}, Problem(err)
	}
	if replayed {
		return Response(f), nil
	}
	work, cancel := context.WithDeadline(ctx, lease)
	defer cancel()
	ref := runfile.Ref{Descriptor: runfile.Descriptor{ID: id, SizeBytes: size, SHA256: digest}, StorageKey: key, PrivateKey: private}
	_, err = runfilestore.Put(work, s.Storage, a.Account.ID, ref, body, size, digest)
	if err != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), api.ExecutionFileGCGrace)
		defer stop()
		_ = s.Store.FailExecutionFileUpload(cleanup, a.Account.ID, id, token, time.Now().UTC())
		return api.ExecutionFileResponse{}, api.NewProblem(http.StatusUnprocessableEntity, "execution_file_transfer_failed", "Run file upload failed", "the transfer did not publish a verified file; retry after cleanup")
	}
	f, err = s.Store.FinishExecutionFileUpload(ctx, a.Account.ID, id, token, time.Now().UTC())
	if err != nil {
		return api.ExecutionFileResponse{}, Problem(err)
	}
	return Response(f), nil
}
func (s Service) Open(ctx context.Context, a Access, id string) (state.ExecutionFile, io.ReadCloser, error) {
	f, err := s.Get(ctx, a, id)
	if err != nil {
		return f, nil, err
	}
	if f.Status != "ready" || !f.ExpiresAt.After(time.Now().UTC()) {
		return f, nil, api.NewProblem(http.StatusGone, "execution_file_expired", "Run file unavailable", "the file is not ready or its retention has expired")
	}
	key, err := runfilestore.OpenKey(a.Account.ID, id, f.StorageKey, f.SealedKey, s.Identities)
	if err != nil {
		return f, nil, api.ErrCapacity("Run file key is unavailable")
	}
	reader, err := runfilestore.Open(ctx, s.Storage, a.Account.ID, runfile.Ref{Descriptor: runfile.Descriptor{ID: id, SizeBytes: f.SizeBytes, SHA256: f.SHA256}, StorageKey: f.StorageKey, PrivateKey: key})
	if err != nil {
		return f, nil, api.ErrCapacity("Run file content is unavailable")
	}
	return f, reader, nil
}
func Problem(err error) *api.Problem {
	var q *state.ExecutionFileQuotaError
	if errors.As(err, &q) {
		if q.ObservedFiles > q.LimitFiles {
			return api.NewProblem(http.StatusTooManyRequests, "execution_file_quota", "Run file storage limit reached", q.Error()).WithLimit(int64(q.LimitFiles), int64(q.ObservedFiles)).WithDocs(api.ExecutionFilesDocsURL)
		}
		return api.NewProblem(http.StatusTooManyRequests, "execution_file_quota", "Run file storage limit reached", q.Error()).WithByteLimit(q.LimitBytes, q.ObservedBytes).WithDocs(api.ExecutionFilesDocsURL)
	}
	if errors.Is(err, state.ErrExecutionFileNotReady) {
		return api.NewProblem(http.StatusConflict, "execution_files_not_ready", "Run files not ready", "one or more input files are not published or have expired; no Run was admitted")
	}
	if errors.Is(err, state.ErrExecutionFileBusy) {
		return api.NewProblem(http.StatusConflict, "execution_file_busy", "Run file busy", "an upload or cleanup already owns this file; retry after it completes")
	}
	return api.ErrCapacity("Run file operation could not be completed")
}

// Collect releases ledger/quota only after the backend acknowledges deletion.
func (s Service) Collect(ctx context.Context, now time.Time) (int, error) {
	if s.Store == nil || s.Storage == nil {
		return 0, nil
	}
	files, err := s.Store.ClaimExpiredExecutionFiles(ctx, now, api.ExecutionFileGCBatchSize)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range files {
		if f.Token == nil || runfile.ValidateKey(f.AccountID, f.ID, f.StorageKey) != nil {
			continue
		}
		err := s.Storage.Delete(ctx, f.StorageKey)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if s.Store.DeleteExecutionFileMetadata(ctx, f.AccountID, f.ID, *f.Token) == nil {
			n++
		}
	}
	return n, nil
}
