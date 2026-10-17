package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

// ObjectNodeSettlement retains placement independently of bucket/account
// lifetime. ReadyAt is zero until the source owner commits terminal settlement.
type ObjectNodeSettlement struct {
	ID, AccountID, AppID, BucketID      string
	Kind, BackendID, BackendFingerprint string
	PhysicalName                        string
	Settlement                          objectrecovery.Settlement
	CreatedAt, ReadyAt, RetryAt         time.Time
	LeaseToken                          string
	LeaseUntil                          time.Time
	Attempts                            int32
	LastError                           string
}

type ObjectNodeSettlementBacklog struct {
	Waiting, Ready int64
	OldestReadyAt  time.Time
}

// ObjectNodeSettlementStore only delivers saved terminal evidence. It cannot
// settle a customer write, release capacity or resolve an uncertain outcome.
type ObjectNodeSettlementStore interface {
	ClaimObjectNodeSettlements(context.Context, string, int32) ([]ObjectNodeSettlement, error)
	FinishObjectNodeSettlement(context.Context, string, string) error
	RetryObjectNodeSettlement(context.Context, string, string, string, time.Duration) error
	ObjectNodeSettlementBacklog(context.Context) (ObjectNodeSettlementBacklog, error)
}

func validNodeSettlementEnrollment(id string, enabled bool) bool {
	_, bounded := objectrecovery.IssuedAt(id)
	return !enabled || bounded
}

func newObjectNodeSettlement(id, kind string, b ObjectBucket, now time.Time) (ObjectNodeSettlement, error) {
	if !validNodeSettlementEnrollment(id, true) || (kind != "put" && kind != "multipart") || b.BackendID == "" || b.BackendFingerprint == "" || b.PhysicalName == "" {
		return ObjectNodeSettlement{}, ErrConflict
	}
	return ObjectNodeSettlement{ID: id, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Kind: kind, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, CreatedAt: now}, nil
}

// UploadNodeSettlement constructs the exact terminal evidence. Prepared
// failures have no node decision and must discard their waiting enrollment.
func UploadNodeSettlement(bucket string, c ObjectUploadCompletion) (objectrecovery.Settlement, bool, error) {
	a := objectrecovery.Settlement{ID: c.ID, Bucket: bucket, Key: c.Key, Kind: "put", Size: c.Bytes}
	if c.WritePhase != ObjectUploadSettled {
		return a, false, ErrConflict
	}
	switch {
	case c.Status == "completed":
		a.Status, a.ETag = "committed", c.ETag
	case c.Status == "failed" && c.ErrorCode == "provider_write_rejected":
		a.Status = "rejected"
	default:
		return a, false, nil
	}
	return a, true, nil
}

// MultipartNodeSettlement uses the frozen selection and checksum, including
// rejected conditional completions only after verified abort has committed.
func MultipartNodeSettlement(bucket string, u ObjectMultipartUpload) (objectrecovery.Settlement, bool, error) {
	a := objectrecovery.Settlement{ID: u.ID, Bucket: bucket, Key: u.Key, Kind: "multipart"}
	if u.ProviderUploadID == "" && u.State == ObjectMultipartAborted {
		return a, false, nil // Initiation was never dispatched.
	}
	if u.ProviderUploadID != objectmultipart.NativeUploadID(u.ID) {
		return a, false, ErrConflict
	}
	if u.State == ObjectMultipartAborted && u.CompletionErrorCode == "" {
		a.Status = "aborted"
		return a, true, nil
	}
	if u.State != ObjectMultipartCompleted && (u.State != ObjectMultipartAborted || u.CompletionErrorCode != "precondition_failed") {
		if u.State == ObjectMultipartAborted {
			return a, false, nil // No matching owned-node completion proof.
		}
		return a, false, ErrConflict
	}
	a.Status, a.Size, a.ETag = "committed", u.SizeBytes, u.CompletionETag
	if u.State == ObjectMultipartAborted {
		a.Status, a.ETag = "rejected", ""
	}
	manifest := objectmultipart.Manifest{UploadID: u.ProviderUploadID, Size: u.SizeBytes, IfMatch: u.CompletionConditions.IfMatch, IfNoneMatch: u.CompletionConditions.IfNoneMatch, Checksum: u.Checksum.Digest}
	for i, part := range u.Parts {
		p := objectmultipart.Part{PartNumber: part.PartNumber, ETag: part.ETag}
		if !u.Checksum.Empty() && i < len(u.Checksum.Parts) {
			p.Checksum = u.Checksum.Parts[i].Value
		}
		manifest.Parts = append(manifest.Parts, p)
	}
	a.ManifestHash = manifest.Hash()
	return a, true, nil
}

func readyObjectNodeSettlement(job ObjectNodeSettlement, a objectrecovery.Settlement, now time.Time) (ObjectNodeSettlement, error) {
	if !job.ReadyAt.IsZero() || a.ID != job.ID || a.Bucket != job.PhysicalName || a.Kind != job.Kind || a.Key == "" || a.Size < 0 || a.Size > api.MaxObjectUploadBytes || !utf8.ValidString(a.Key) {
		return job, ErrConflict
	}
	if a.Status == "committed" {
		if strings.TrimSpace(a.ETag) == "" || len(a.ETag) > api.MaxObjectWriteETagBytes || strings.ContainsAny(a.ETag, "\r\n\x00") {
			return job, ErrConflict
		}
	} else if (a.Status != "rejected" && a.Status != "aborted") || a.ETag != "" {
		return job, ErrConflict
	}
	if a.Kind == "put" && (a.ManifestHash != "" || a.Status == "aborted") || a.Kind == "multipart" && a.Status != "aborted" && len(a.ManifestHash) != 64 || a.Status == "aborted" && (a.Size != 0 || a.ManifestHash != "") {
		return job, ErrConflict
	}
	body, err := json.Marshal(a)
	if err != nil || len(body) > api.ObjectNodeRecoveryRequestMaxBytes {
		return job, ErrConflict
	}
	job.Settlement, job.ReadyAt, job.RetryAt = a, now, now
	return job, nil
}

func validNodeSettlementClaim(token string, limit int32) bool {
	u, err := uuid.Parse(token)
	return err == nil && u != uuid.Nil && u.String() == token && limit > 0 && limit <= api.ObjectNodeSettlementBatch
}

func validNodeSettlementRetry(code string, delay time.Duration) bool {
	return (code == "configuration" || code == "unavailable") && delay >= api.ObjectNodeSettlementRetryMin && delay <= api.ObjectNodeSettlementRetryMax && delay%time.Second == 0
}
