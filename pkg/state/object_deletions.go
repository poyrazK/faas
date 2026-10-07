package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type ObjectDeletion struct {
	api.ObjectDeletion
	NativeGCS                                                  bool                            `json:"-"`
	RecoveryClaimed                                            bool                            `json:"-"`
	AccountID, AppID, Token, ProviderStatus, ProviderVersionID string                          `json:"-"`
	TargetProviderVersionID                                    string                          `json:"-"`
	Baseline                                                   []string                        `json:"-"`
	LeaseUntil, RetryAt                                        time.Time                       `json:"-"`
	ReservedBytes                                              int64                           `json:"-"`
	Lifecycle                                                  *ObjectLifecycleDeletionBinding `json:"-"`
	ProtectionRequired, ProtectionVerified, DeletionVerified   bool                            `json:"-"`
}

// Protected lifecycle dispatch requires fresh native policy reads. A deletion
// acknowledgment cannot complete these receipts without exact absence proof.
type ObjectProtectedLifecycleDeletionStore interface {
	DispatchObjectProtectedLifecycleDeletion(context.Context, string, string, string, []string) (ObjectDeletion, error)
}

type ObjectDeletionStore interface {
	BeginObjectDeletion(context.Context, ObjectDeletion, api.ObjectStoragePolicy) (ObjectDeletion, bool, error)
	GetObjectDeletion(context.Context, string, string, string) (ObjectDeletion, error)
	DispatchObjectDeletion(context.Context, string, string, string, []string) (ObjectDeletion, error)
	FinishObjectDeletion(context.Context, ObjectDeletion) (ObjectDeletion, error)
	DueObjectDeletions(context.Context, int32) ([]ObjectDeletion, error)
	ClaimObjectDeletion(context.Context, string, string) (ObjectDeletion, error)
	RetryObjectDeletion(context.Context, string, string, string) error
}

// ObjectDeletionActivityStore supplies an owned planning check. Admission and
// dispatch still acquire the authoritative mutation fence atomically.
type ObjectDeletionActivityStore interface {
	HasActiveObjectDeletion(context.Context, string, string, string) (bool, error)
}

func newDeletionIntent(j ObjectDeletion) ObjectDeletion {
	return ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: j.ID, BucketID: j.BucketID, Key: j.Key, Selector: j.Selector}, AccountID: j.AccountID, AppID: j.AppID, Token: j.Token, NativeGCS: j.NativeGCS, Lifecycle: cloneLifecycleDeletionBinding(j.Lifecycle)}
}

func deletionActive(j ObjectDeletion) bool    { return j.State == "prepared" || j.State == "dispatched" }
func immutableDeletion(j ObjectDeletion) bool { return j.Selector != "" && j.Selector != "null" }
func lifecycleProtectionRequired(j ObjectDeletion, lock ObjectBucketObjectLock) bool {
	return j.Lifecycle != nil && j.Selector != "" && (lock.EnabledRequired || lock.NativeEnabledObserved || lock.ObservedConfiguration != nil && lock.ObservedConfiguration.Enabled)
}
func cloneDeletion(j ObjectDeletion) ObjectDeletion {
	j.Baseline = append([]string{}, j.Baseline...)
	j.Lifecycle = cloneLifecycleDeletionBinding(j.Lifecycle)
	return j
}
func validDeletionIdentity(j ObjectDeletion) bool {
	for _, id := range []string{j.ID, j.AccountID, j.AppID, j.BucketID} {
		_, e := uuid.Parse(id)
		if e != nil {
			return false
		}
	}
	return j.Key != "" && len(j.Key) <= api.MaxObjectS3ListTextBytes && utf8.ValidString(j.Key) && !strings.ContainsRune(j.Key, 0) && (j.Selector == "" || ValidObjectVersionID(j.Selector)) && j.Token != "" && len(j.Token) <= 128 && validLifecycleDeletionBinding(j.Lifecycle, j.Selector)
}
func validDeletionLease(j ObjectDeletion, token string, now time.Time) bool {
	return deletionActive(j) && token != "" && j.Token == token && j.LeaseUntil.After(now)
}
func validDeletionBaseline(b []string) bool {
	if len(b) > api.ObjectDeletionHistoryVersions {
		return false
	}
	seen := map[string]bool{}
	for _, s := range b {
		v, e := hex.DecodeString(s)
		if e != nil || len(v) != 32 || strings.ToLower(s) != s || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func dispatchVerifiedDeletion(j ObjectDeletion, token, status string, baseline []string, verified bool, now time.Time) (ObjectDeletion, error) {
	if j.ProtectionRequired && (!verified || j.Lifecycle == nil || j.Lifecycle.ExpectedDeleteMarker == nil) {
		return j, ErrConflict
	}
	if !validDeletionLease(j, token, now) || j.State != "prepared" || !validObjectDeletionProviderStatus(status) || !validDeletionBaseline(baseline) || status != "Enabled" && !nativeGCSDeletionStatus(status) && len(baseline) != 0 {
		return j, ErrConflict
	}
	if status != j.ProviderStatus || nativeGCSDeletionStatus(status) && (j.Selector != "" || j.ReservedBytes != 0 || !validGCSDeletionBaseline(baseline)) {
		return j, ErrConflict
	}
	j.State = "dispatched"
	j.ProtectionVerified = j.ProtectionRequired && verified
	j.ProviderStatus = status
	j.Baseline = append([]string{}, baseline...)
	j.UpdatedAt = now
	return j, nil
}
func finishDeletion(j, result ObjectDeletion, now time.Time) (ObjectDeletion, error) {
	if !validDeletionLease(j, result.Token, now) {
		return j, ErrConflict
	}
	switch result.State {
	case "failed":
		prepared := j.State == "prepared" && (result.LastErrorCode == "preparation_failed" || result.LastErrorCode == "preparation_expired" || j.ProtectionRequired && result.LastErrorCode == "object_protected")
		rejected := j.State == "dispatched" && !j.RecoveryClaimed && result.LastErrorCode == "provider_rejected"
		if !prepared && !rejected || result.ProviderVersionID != "" || result.VersionID != "" || result.DeleteMarker {
			return j, ErrConflict
		}
	case "completed":
		if j.ProtectionRequired && (!result.DeletionVerified || j.Lifecycle == nil || j.Lifecycle.ExpectedDeleteMarker == nil || result.DeleteMarker != *j.Lifecycle.ExpectedDeleteMarker) {
			return j, ErrConflict
		}
		if j.State != "dispatched" || result.LastErrorCode != "" || result.VersionID != "" && !ValidObjectVersionID(result.VersionID) {
			return j, ErrConflict
		}
		if immutableDeletion(j) {
			if result.VersionID != j.Selector || result.ProviderVersionID != j.TargetProviderVersionID {
				return j, ErrConflict
			}
			break
		}
		if nativeGCSDeletionStatus(j.ProviderStatus) {
			if j.Selector != "" || result.DeleteMarker || result.VersionID != "" || result.ProviderVersionID != "" {
				return j, ErrConflict
			}
			break
		}
		if j.Selector == "null" && (result.VersionID != "null" || result.ProviderVersionID != "null") || j.ProviderStatus == "Enabled" && j.Selector == "" && (!result.DeleteMarker || result.VersionID == "" || result.VersionID == "null" || result.ProviderVersionID == "") {
			return j, ErrConflict
		}
		if j.Selector == "" && j.ProviderStatus == "Suspended" && (!result.DeleteMarker || result.VersionID != "null" || result.ProviderVersionID != "null") || j.Selector == "" && j.ProviderStatus == "" && (result.DeleteMarker || result.VersionID != "" || result.ProviderVersionID != "") {
			return j, ErrConflict
		}
	default:
		return j, ErrConflict
	}
	j.State = result.State
	j.LastErrorCode = result.LastErrorCode
	j.VersionID = result.VersionID
	j.DeleteMarker = result.DeleteMarker
	j.ProviderVersionID = result.ProviderVersionID
	j.DeletionVerified = j.ProtectionRequired && result.State == "completed" && result.DeletionVerified
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	return j, nil
}

// Native GCS deletion has a captured generation fence, not an S3 marker.
func nativeGCSDeletionStatus(status string) bool {
	return status == "GCS_Enabled" || status == "GCS_Suspended"
}

func validGCSDeletionBaseline(b []string) bool {
	return len(b) == 1 && len(b[0]) == 64 && strings.Trim(b[0][:48], "0") == "" && b[0][48] <= '7' && validDeletionBaseline(b)
}
func validObjectDeletionProviderStatus(status string) bool {
	return status == "" || ValidObjectBucketVersioningStatus(status) || nativeGCSDeletionStatus(status)
}
