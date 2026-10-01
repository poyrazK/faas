package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectCapacityStore journals single-attempt proxy writes before dispatch.
// Expiry or cancellation never settles an uncertain provider request.
type ObjectCapacityStore interface {
	BeginObjectWrite(context.Context, string, string, string, string, int64, api.ObjectStoragePolicy) error
	SettleObjectWrite(context.Context, string, string, string) error
	RequestObjectCapacityReconciliation(context.Context, string, string, string) (ObjectCapacityReconciliation, error)
	GetObjectCapacityReconciliation(context.Context, string, string, string) (ObjectCapacityReconciliation, error)
	CancelObjectCapacityReconciliation(context.Context, string, string, string) (ObjectCapacityReconciliation, error)
	DueObjectCapacityReconciliations(context.Context, int32) ([]ObjectCapacityReconciliation, error)
	ClaimObjectCapacityReconciliation(context.Context, string, string) (ObjectCapacityReconciliation, error)
	FinishObjectCapacityReconciliation(context.Context, string, string, int64, int64) (ObjectCapacityReconciliation, error)
	RetryObjectCapacityReconciliation(context.Context, string, string) error
}

type ObjectCapacityReconciliation struct {
	api.ObjectCapacityReconciliation
	AccountID, AppID, Token         string
	LeaseUntil, RetryAt, DeadlineAt time.Time
}

type objectWriteAdmission struct {
	BucketID, KeyHash, MultipartID string
	Settled, Route                 bool
}

func objectCapacityActive(s string) bool { return s == "waiting" || s == "scanning" }
func objectCapacityTotals(s ObjectUsageSnapshot, bucket string) (int64, int64) {
	for _, u := range s.Buckets {
		if u.Bucket.ID == bucket {
			return max(u.ObservedBytes, boundedObjectAdd(u.BaselineBytes, u.GrantedBytes)) + u.MultipartBytes, max(u.ObservedKeys, boundedObjectAdd(u.BaselineKeys, u.GrantedKeys))
		}
	}
	return 0, 0
}
func prepareObjectCapacityClaim(j ObjectCapacityReconciliation, token string, pending int64, unsafe, multipart, versions bool, now time.Time) ObjectCapacityReconciliation {
	j.UpdatedAt = now
	j.PendingWrites = pending
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.RetryAt = now.Add(api.ObjectCapacityReconciliationRetry)
	j.State = "waiting"
	j.LastErrorCode = ""
	switch {
	case !j.DeadlineAt.After(now):
		j.State = "failed"
		j.LastErrorCode = "deadline"
	case versions:
		j.State = "blocked"
		j.LastErrorCode = "version_accounting_required"
	case unsafe:
		j.State = "blocked"
		j.LastErrorCode = "untracked_writes"
	case multipart:
		j.LastErrorCode = "multipart_active"
	case pending > 0:
		j.LastErrorCode = "unsettled_writes"
	default:
		j.State = "scanning"
		j.Token = token
		j.LeaseUntil = now.Add(api.ObjectCapacityReconciliationLease)
	}
	if !objectCapacityActive(j.State) {
		j.FinishedAt = &now
	}
	return j
}
func completeObjectCapacityJob(j ObjectCapacityReconciliation, bytes, keys int64, now time.Time) ObjectCapacityReconciliation {
	j.State = "completed"
	j.AfterBytes = bytes
	j.AfterKeys = keys
	j.ReclaimedBytes = max(int64(0), j.BeforeBytes-bytes)
	j.ReclaimedKeys = max(int64(0), j.BeforeKeys-keys)
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	j.FinishedAt = &now
	j.LastErrorCode = ""
	return j
}
