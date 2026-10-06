package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ObjectBucketObjectLock struct {
	api.ObjectBucketObjectLock
	AccountID, AppID, Token           string    `json:"-"`
	LeaseUntil, RetryAt               time.Time `json:"-"`
	Dispatched, NativeEnabledObserved bool      `json:"-"`
}

type ObjectBucketObjectLockStore interface {
	GetObjectBucketObjectLock(context.Context, string, string, string) (ObjectBucketObjectLock, error)
	RequestObjectBucketObjectLock(context.Context, string, string, string, api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error)
	ObserveObjectBucketObjectLock(context.Context, string, string, string, int64, api.ObjectBucketObjectLockConfiguration, bool) (ObjectBucketObjectLock, error)
	DueObjectBucketObjectLock(context.Context, int32) ([]ObjectBucketObjectLock, error)
	ClaimObjectBucketObjectLock(context.Context, string, string) (ObjectBucketObjectLock, error)
	DispatchObjectBucketObjectLock(context.Context, string, string) (ObjectBucketObjectLock, error)
	FinishObjectBucketObjectLock(context.Context, string, string, api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error)
	RetryObjectBucketObjectLock(context.Context, string, string, string) error
}

func cloneObjectBucketObjectLock(j ObjectBucketObjectLock) ObjectBucketObjectLock {
	if j.ObservedConfiguration != nil {
		c := j.ObservedConfiguration.Clone()
		j.ObservedConfiguration = &c
	}
	if j.DesiredConfiguration != nil {
		c := j.DesiredConfiguration.Clone()
		j.DesiredConfiguration = &c
	}
	return j
}

func ViewObjectBucketObjectLock(j ObjectBucketObjectLock) api.ObjectBucketObjectLock {
	return cloneObjectBucketObjectLock(j).ObjectBucketObjectLock
}
func objectLockActive(j ObjectBucketObjectLock) bool { return j.State != "" && j.State != "ready" }
func newObjectBucketObjectLock(b ObjectBucket, now time.Time) ObjectBucketObjectLock {
	return ObjectBucketObjectLock{ObjectBucketObjectLock: api.ObjectBucketObjectLock{BucketID: b.ID, State: "ready", UpdatedAt: now}, AccountID: b.AccountID, AppID: b.AppID, RetryAt: now}
}

func requestObjectBucketObjectLock(j ObjectBucketObjectLock, c api.ObjectBucketObjectLockConfiguration, now time.Time) (ObjectBucketObjectLock, error) {
	if !c.Enabled || !c.Valid() {
		return j, ErrConflict
	}
	if j.DesiredConfiguration != nil && j.DesiredConfiguration.Equal(c) {
		return cloneObjectBucketObjectLock(j), nil
	}
	initialObservation := j.Revision == 0 && j.DesiredConfiguration == nil && !j.LeaseUntil.After(now)
	if objectLockActive(j) && !initialObservation || j.Revision >= api.MaxObjectBucketObjectLockRevision {
		return j, ErrConflict
	}
	j.Revision++
	c = c.Clone()
	j.DesiredConfiguration = &c
	j.EnabledRequired = true
	j.State, j.Token, j.LeaseUntil, j.Dispatched = "waiting", "", time.Time{}, false
	j.LastErrorCode = ""
	j.UpdatedAt, j.RetryAt = now, now
	return cloneObjectBucketObjectLock(j), nil
}

func observeObjectBucketObjectLock(j ObjectBucketObjectLock, revision int64, c api.ObjectBucketObjectLockConfiguration, known, versioningReady bool, now time.Time) (ObjectBucketObjectLock, error) {
	if revision != j.Revision || known && !c.Valid() {
		return j, ErrConflict
	}
	changed := known && (j.ObservedConfiguration == nil || !j.ObservedConfiguration.Equal(c))
	j.ObservedKnown = known
	j.EnabledRequired = j.EnabledRequired || c.Enabled
	j.NativeEnabledObserved = j.NativeEnabledObserved || c.Enabled
	j.ObservedConfiguration = nil
	if known {
		c = c.Clone()
		j.ObservedConfiguration = &c
	}
	if j.State == "applying" && (!known || j.EnabledRequired && !versioningReady) {
		j.State = "waiting"
		j.Token = ""
		j.LeaseUntil = time.Time{}
	}
	if !objectLockActive(j) && (!known || j.EnabledRequired && (!c.Enabled || !versioningReady) ||
		j.DesiredConfiguration != nil && !j.DesiredConfiguration.Equal(c) ||
		j.DesiredConfiguration == nil && c.Enabled && changed) {
		j.State = "waiting"
		j.RetryAt = now
		j.Token = ""
		j.LeaseUntil = time.Time{}
	}
	if !known {
		j.RetryAt = now.Add(api.ObjectBucketObjectLockRetry)
		j.LastErrorCode = "provider_failed"
	}
	j.UpdatedAt = now
	return cloneObjectBucketObjectLock(j), nil
}

// Only a native Enabled observation can supersede an unsafe pending suspension.
// Preserve a running inventory job so its worker can drain; the fresh propagation
// interval still requires another inventory before returning to ready.
func requireObjectLockVersioning(v ObjectBucketVersioning, lock ObjectBucketObjectLock, now time.Time) (ObjectBucketVersioning, error) {
	if !lock.EnabledRequired {
		return v, nil
	}
	if versioningActive(v) && v.DesiredStatus == "Suspended" {
		if !lock.NativeEnabledObserved {
			return v, ErrConflict
		}
		v.Revision++
		v.DesiredStatus = "Enabled"
		v.ObservedStatus = "Enabled"
		v.Token, v.LeaseUntil, v.Dispatched = "", time.Time{}, false
		v.LastErrorCode = ""
		if v.State != "inventory" {
			v.State = "waiting"
			v.CapacityJobID = ""
		}
		v.VersionsRequired = true
		t := now.Add(api.ObjectBucketVersioningPropagation)
		v.PropagationUntil = &t
		v.RetryAt, v.UpdatedAt = now, now
		return v, nil
	}
	if lock.NativeEnabledObserved && !v.VersionsRequired {
		v = observeObjectVersioning(v, "Enabled", now)
	}
	return requestObjectVersioning(v, "Enabled", now)
}

func objectLockVersioningReady(v ObjectBucketVersioning) bool {
	return v.State == "ready" && v.DesiredStatus == "Enabled" && v.ObservedStatus == "Enabled" && v.VersionsRequired && v.CapacityJobID != ""
}

func claimObjectBucketObjectLock(j ObjectBucketObjectLock, token string, versioningReady bool, pending int64, unsafe, multipart, capacity bool, now time.Time) (ObjectBucketObjectLock, error) {
	if !objectLockActive(j) || token == "" || len(token) > api.MaxObjectLockLeaseTokenBytes || j.RetryAt.After(now) || j.LeaseUntil.After(now) {
		return j, ErrConflict
	}
	j.Token, j.LeaseUntil, j.State = "", time.Time{}, "waiting"
	j.UpdatedAt, j.RetryAt = now, now.Add(api.ObjectBucketObjectLockRetry)
	j.LastErrorCode = ""
	switch {
	case j.EnabledRequired && !versioningReady:
		j.LastErrorCode = "versioning_pending"
	case objectLockNeedsDrain(j) && unsafe:
		j.LastErrorCode = "untracked_writes"
	case objectLockNeedsDrain(j) && pending > 0:
		j.LastErrorCode = "unsettled_writes"
	case objectLockNeedsDrain(j) && multipart:
		j.LastErrorCode = "multipart_active"
	case objectLockNeedsDrain(j) && capacity:
		j.LastErrorCode = "capacity_active"
	default:
		j.State, j.Token, j.LeaseUntil = "applying", token, now.Add(api.ObjectBucketObjectLockLease)
	}
	return cloneObjectBucketObjectLock(j), nil
}

func validObjectLockLease(j ObjectBucketObjectLock, token string, now time.Time) bool {
	return j.State == "applying" && token != "" && j.Token == token && j.LeaseUntil.After(now)
}

func finishObjectBucketObjectLock(j ObjectBucketObjectLock, token string, c api.ObjectBucketObjectLockConfiguration, versioningReady bool, now time.Time) (ObjectBucketObjectLock, error) {
	if !validObjectLockLease(j, token, now) || !c.Valid() || j.EnabledRequired && (!c.Enabled || !versioningReady) || j.DesiredConfiguration != nil && !j.DesiredConfiguration.Equal(c) {
		return j, ErrConflict
	}
	c = c.Clone()
	j.ObservedConfiguration = &c
	j.ObservedKnown = true
	j.EnabledRequired = j.EnabledRequired || c.Enabled
	j.NativeEnabledObserved = j.NativeEnabledObserved || c.Enabled
	j.State, j.Token, j.LeaseUntil, j.LastErrorCode = "ready", "", time.Time{}, ""
	j.UpdatedAt, j.RetryAt = now, now
	return cloneObjectBucketObjectLock(j), nil
}

func retryObjectBucketObjectLock(j ObjectBucketObjectLock, token, code string, now time.Time) (ObjectBucketObjectLock, error) {
	if !validObjectLockLease(j, token, now) || code != "provider_failed" && code != "provider_mismatch" && code != "provider_unsupported" && code != "versioning_pending" {
		return j, ErrConflict
	}
	j.State, j.Token, j.LeaseUntil, j.LastErrorCode = "waiting", "", time.Time{}, code
	j.UpdatedAt, j.RetryAt = now, now.Add(api.ObjectBucketObjectLockRetry)
	return cloneObjectBucketObjectLock(j), nil
}

func orderObjectBucketObjectLockJobs(jobs []ObjectBucketObjectLock, limit int32) []ObjectBucketObjectLock {
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].RetryAt.Before(jobs[j].RetryAt) || jobs[i].RetryAt.Equal(jobs[j].RetryAt) && jobs[i].BucketID < jobs[j].BucketID
	})
	return jobs[:min(len(jobs), int(limit))]
}

// A verified disabled, unenrolled policy needs no mutation or transfer drain.
func objectLockNeedsDrain(j ObjectBucketObjectLock) bool {
	return j.EnabledRequired || j.DesiredConfiguration != nil || !j.ObservedKnown || j.ObservedConfiguration == nil || j.ObservedConfiguration.Enabled
}
