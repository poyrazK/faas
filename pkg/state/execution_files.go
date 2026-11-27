package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

var (
	ErrExecutionFileNotReady  = errors.New("state: execution file is not ready")
	ErrExecutionFileBusy      = errors.New("state: execution file upload is already in progress")
	ErrExecutionFileLeaseLost = errors.New("state: execution file lease was lost")
)

type ExecutionFile struct {
	ID             string     `json:"-"`
	AccountID      string     `json:"-"`
	PrincipalID    *string    `json:"-"`
	ExecutionID    *string    `json:"-"`
	Name           string     `json:"-"`
	Kind           string     `json:"-"`
	Status         string     `json:"-"`
	SizeBytes      int64      `json:"-"`
	SHA256         string     `json:"-"`
	StorageKey     string     `json:"-"`
	SealedKey      []byte     `json:"-"`
	Token          *string    `json:"-"`
	LeaseExpiresAt *time.Time `json:"-"`
	ExpiresAt      time.Time  `json:"-"`
	CreatedAt      time.Time  `json:"-"`
	UpdatedAt      time.Time  `json:"-"`
}

type ExecutionFileQuotaError struct {
	LimitBytes, ObservedBytes int64
	LimitFiles, ObservedFiles int
}

func (e *ExecutionFileQuotaError) Error() string {
	return "execution file storage or object-count limit exceeded"
}

// ExecutionFileStore is independent of Store so old test doubles remain small.
// Claim reserves bytes/count and the single upload attempt atomically. A ready
// immutable object returns replay=true and must never be overwritten.
type ExecutionFileStore interface {
	ExecutionFileByID(context.Context, string, string) (ExecutionFile, error)
	ClaimExecutionFileUpload(context.Context, ExecutionFile) (file ExecutionFile, replay bool, err error)
	FinishExecutionFileUpload(context.Context, string, string, string, time.Time) (ExecutionFile, error)
	FailExecutionFileUpload(context.Context, string, string, string, time.Time) error
	RevokeExecutionFile(context.Context, string, string, *string, bool, time.Time) error
	ClaimExpiredExecutionFiles(context.Context, time.Time, int) ([]ExecutionFile, error)
	DeleteExecutionFileMetadata(context.Context, string, string, string) error
}

// The PostgreSQL implementation performs this read on its admission tx.
type ExecutionFileAdmissionReader interface {
	ExecutionFileByID(context.Context, string, string) (ExecutionFile, error)
}

func cloneExecutionFile(f ExecutionFile) ExecutionFile {
	f.PrincipalID = cloneStringPtr(f.PrincipalID)
	f.ExecutionID = cloneStringPtr(f.ExecutionID)
	f.Token = cloneStringPtr(f.Token)
	f.SealedKey = append([]byte(nil), f.SealedKey...)
	if f.LeaseExpiresAt != nil {
		v := *f.LeaseExpiresAt
		f.LeaseExpiresAt = &v
	}
	return f
}

func validateExecutionFileUpload(f ExecutionFile) error {
	if !runfile.ValidID(f.ID) || !runfile.ValidOwnerID(f.AccountID) || f.PrincipalID != nil && !runfile.ValidOwnerID(*f.PrincipalID) || f.ExecutionID != nil || f.Kind != "upload" || f.Status != "uploading" || f.Name != "" || f.Token == nil || !runfile.ValidID(*f.Token) || f.LeaseExpiresAt == nil || !f.LeaseExpiresAt.After(f.UpdatedAt) || !f.ExpiresAt.After(f.UpdatedAt) || f.SizeBytes < 0 || f.SizeBytes > api.ExecutionFileHardMaxBytes || !runfile.ValidDigest(f.SHA256) || len(f.SealedKey) == 0 || len(f.SealedKey) > api.ExecutionFileKeyMaxBytes {
		return ErrExecutionInvalid
	}
	return runfile.ValidateKey(f.AccountID, f.ID, f.StorageKey)
}

func executionFileOwner(f ExecutionFile, principal *string, wide bool) bool {
	return wide || f.PrincipalID == nil && principal == nil || f.PrincipalID != nil && principal != nil && *f.PrincipalID == *principal
}

func validateExecutionFileQuota(limits api.ExecutionFileLimits, bytes int64, count int) error {
	if bytes > limits.MaxStoredBytes || count > limits.MaxFiles {
		return &ExecutionFileQuotaError{limits.MaxStoredBytes, bytes, limits.MaxFiles, count}
	}
	return nil
}

func storedInputBytes(refs []runfile.Ref) int64 {
	var n int64
	for _, ref := range refs {
		n += ref.SizeBytes
	}
	return n
}

func outputExecutionFiles(params CreateExecutionParams, id string) []ExecutionFile {
	files := make([]ExecutionFile, 0, len(params.Request.StoredOutputs))
	for _, ref := range params.Request.StoredOutputs {
		origin := id
		deadline := params.DeadlineAt.UTC()
		files = append(files, ExecutionFile{ID: ref.ID, AccountID: params.AccountID, PrincipalID: cloneStringPtr(params.RunsPrincipalID), ExecutionID: &origin, Name: ref.Path, Kind: "output", Status: "pending", StorageKey: ref.StorageKey, SealedKey: append([]byte(nil), ref.SealedKey...), LeaseExpiresAt: &deadline, ExpiresAt: params.AdmittedAt.Add(api.ExecutionFileRetention), CreatedAt: params.AdmittedAt, UpdatedAt: params.AdmittedAt})
	}
	return files
}

func validateStoredOutputs(params CreateExecutionParams) error {
	refs := params.Request.StoredOutputs
	if len(refs) > api.ExecutionArtifactMaxFiles || len(refs) > 0 && params.Request.Limits.MaxArtifactBytes <= 0 {
		return ErrExecutionInvalid
	}
	if len(refs) > 0 && len(refs) != len(params.Request.OutputFiles) {
		return ErrExecutionInvalid
	}
	ids := map[string]bool{}
	for i, ref := range refs {
		if ids[ref.ID] || ref.Path != params.Request.OutputFiles[i] || ref.Bundle || ref.SizeBytes != 0 || ref.SHA256 != "" || len(ref.SealedKey) == 0 || len(ref.SealedKey) > api.ExecutionFileKeyMaxBytes || ref.PrivateKey == "" {
			return ErrExecutionInvalid
		}
		if err := runfile.ValidateKey(params.AccountID, ref.ID, ref.StorageKey); err != nil {
			return fmt.Errorf("%w: invalid output object scope", ErrExecutionInvalid)
		}
		ids[ref.ID] = true
	}
	return nil
}
