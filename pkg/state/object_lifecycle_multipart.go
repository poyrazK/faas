package state

import (
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// An immutable, private receipt of admission. Recovery owns the abort after
// admission, even if the scan is cancelled or the rule subsequently changes.
type ObjectLifecycleMultipartBinding struct {
	ScanID                   string    `json:"scan_id"`
	ScanToken                string    `json:"scan_token"`
	RuleID                   string    `json:"rule_id"`
	ExpectedProviderUploadID string    `json:"expected_provider_upload_id"`
	ExpectedCreatedAt        time.Time `json:"expected_created_at"`
}

func lifecycleMultipartCandidate(j ObjectLifecycleScan, u ObjectMultipartUpload) bool {
	return u.AccountID == j.AccountID && u.AppID == j.AppID && u.BucketID == j.BucketID && u.State == ObjectMultipartActive && u.ProviderUploadID != "" && !u.CreatedAt.After(j.CreatedAt) && u.ID > j.LastUploadID
}

func checkpointLifecycleMultipart(j ObjectLifecycleScan, token string, old, expected ObjectMultipartUpload, now time.Time) (ObjectLifecycleScan, ObjectMultipartUpload, error) {
	_, idErr := uuid.Parse(expected.ID)
	if !validLifecycleScanLease(j, token, now) || j.Phase != "multipart" || idErr != nil || old.ID != expected.ID || old.ID <= j.LastUploadID || old.AccountID != j.AccountID || old.AppID != j.AppID || old.BucketID != j.BucketID || old.AccountID != expected.AccountID || old.AppID != expected.AppID || old.BucketID != expected.BucketID || old.Key != expected.Key || old.ProviderUploadID != expected.ProviderUploadID || !old.CreatedAt.Equal(expected.CreatedAt) || old.CreatedAt.After(j.CreatedAt) || j.ScannedUploads >= api.MaxObjectStoragePolicyValue {
		return j, old, ErrConflict
	}
	if old.State == ObjectMultipartActive {
		if old.LeaseUntil.After(now) {
			return j, old, ErrConflict
		}
		rule, err := api.ObjectLifecycleMultipartAbortRule(j.Rules, old.Key, old.CreatedAt, now)
		if err != nil {
			return j, old, ErrConflict
		}
		if rule != "" {
			old.State, old.LeaseToken, old.LeaseUntil = ObjectMultipartAborting, "", time.Time{}
			old.AttemptCount, old.LastErrorCode, old.RetryAt, old.UpdatedAt = 0, "", now, now
			old.LifecycleAbort = ObjectLifecycleMultipartBinding{ScanID: j.ID, ScanToken: token, RuleID: rule, ExpectedProviderUploadID: old.ProviderUploadID, ExpectedCreatedAt: old.CreatedAt}
		}
	}
	j.LastUploadID, j.ScannedUploads = old.ID, j.ScannedUploads+1
	j.Token, j.LeaseUntil, j.RetryAt, j.UpdatedAt = "", time.Time{}, now, now
	return j, old, nil
}
