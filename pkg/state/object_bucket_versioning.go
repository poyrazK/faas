package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

// A transition cannot be cancelled: an uncertain provider request may apply.
// Opposite targets are accepted only after the preceding cutover is ready.
type ObjectBucketVersioningStore interface {
	RequestObjectBucketVersioning(context.Context, string, string, string, string) (ObjectBucketVersioning, error)
	ObserveObjectBucketVersioning(context.Context, string, string, string, string) (ObjectBucketVersioning, error)
	GetObjectBucketVersioning(context.Context, string, string, string) (ObjectBucketVersioning, error)
	DueObjectBucketVersioning(context.Context, int32) ([]ObjectBucketVersioning, error)
	ClaimObjectBucketVersioning(context.Context, string, string) (ObjectBucketVersioning, error)
	DispatchObjectBucketVersioning(context.Context, string, string) (ObjectBucketVersioning, error)
	AdvanceObjectBucketVersioning(context.Context, string, string, string) (ObjectBucketVersioning, error)
	RetryObjectBucketVersioning(context.Context, string, string) error
}

type ObjectBucketVersioning struct {
	api.ObjectBucketVersioning
	AccountID, AppID, Token string    `json:"-"`
	LeaseUntil, RetryAt     time.Time `json:"-"`
	Dispatched              bool      `json:"-"`
}

func ValidObjectBucketVersioningStatus(s string) bool { return s == "Enabled" || s == "Suspended" }
func versioningActive(j ObjectBucketVersioning) bool  { return j.State != "ready" }
func cloneObjectVersioning(j ObjectBucketVersioning) ObjectBucketVersioning {
	if j.PropagationUntil != nil {
		t := *j.PropagationUntil
		j.PropagationUntil = &t
	}
	return j
}
func newObjectVersioning(b ObjectBucket, now time.Time) ObjectBucketVersioning {
	return ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: b.ID, State: "ready", UpdatedAt: now}, AccountID: b.AccountID, AppID: b.AppID, RetryAt: now}
}
func requestObjectVersioning(j ObjectBucketVersioning, status string, now time.Time) (ObjectBucketVersioning, error) {
	if !ValidObjectBucketVersioningStatus(status) {
		return j, ErrConflict
	}
	if versioningActive(j) {
		if j.DesiredStatus != status {
			return j, ErrConflict
		}
		return j, nil
	}
	if j.DesiredStatus == status {
		return j, nil
	}
	j.DesiredStatus = status
	j.Revision++
	j.State = "waiting"
	j.CapacityJobID = ""
	j.Dispatched = false
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.RetryAt = now
	j.UpdatedAt = now
	j.LastErrorCode = ""
	return j, nil
}
func observeObjectVersioning(j ObjectBucketVersioning, status string, now time.Time) ObjectBucketVersioning {
	j.ObservedStatus = status
	j.UpdatedAt = now
	if status != "" && !j.VersionsRequired {
		j.VersionsRequired = true
		t := now.Add(api.ObjectBucketVersioningPropagation)
		j.PropagationUntil = &t
	}
	if !j.LeaseUntil.After(now) && j.State == "ready" && (j.DesiredStatus != status || status != "" && j.CapacityJobID == "") {
		if j.DesiredStatus == "" {
			j.DesiredStatus = status
			j.Revision++
		}
		j.State = "waiting"
		j.RetryAt = now
	}
	return j
}
func validVersioningLease(j ObjectBucketVersioning, token string, now time.Time) bool {
	return versioningActive(j) && token != "" && j.Token == token && j.LeaseUntil.After(now)
}
func claimObjectVersioning(j ObjectBucketVersioning, token string, pending int64, unsafe, multipart, capacity bool, now time.Time) (ObjectBucketVersioning, error) {
	if token == "" || !versioningActive(j) || j.RetryAt.After(now) || j.LeaseUntil.After(now) {
		return j, ErrConflict
	}
	j.LastErrorCode = ""
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	j.RetryAt = now.Add(api.ObjectBucketVersioningRetry)
	switch {
	case unsafe:
		j.LastErrorCode = "untracked_writes"
	case pending > 0:
		j.LastErrorCode = "unsettled_writes"
	case multipart:
		j.LastErrorCode = "multipart_active"
	case capacity:
		j.LastErrorCode = "capacity_active"
	default:
		j.Token = token
		j.LeaseUntil = now.Add(api.ObjectBucketVersioningLease)
	}
	return j, nil
}
func releaseObjectVersioning(j ObjectBucketVersioning, now time.Time) ObjectBucketVersioning {
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.RetryAt = now.Add(api.ObjectBucketVersioningRetry)
	j.UpdatedAt = now
	return j
}
func advanceObjectVersioning(j ObjectBucketVersioning, status string, inventoryDone bool, now time.Time) (ObjectBucketVersioning, bool) {
	j = observeObjectVersioning(j, status, now)
	if status != j.DesiredStatus {
		j.State = "waiting"
		j.LastErrorCode = "provider_mismatch"
		return releaseObjectVersioning(j, now), false
	}
	if j.PropagationUntil != nil && j.PropagationUntil.After(now) {
		j.State = "propagating"
		j.LastErrorCode = ""
		j = releaseObjectVersioning(j, now)
		j.RetryAt = *j.PropagationUntil
		return j, false
	}
	if j.State == "inventory" && inventoryDone {
		j.State = "ready"
		j.LastErrorCode = ""
		return releaseObjectVersioning(j, now), false
	}
	j.State = "inventory"
	j.LastErrorCode = ""
	return releaseObjectVersioning(j, now), true
}
