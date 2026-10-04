package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectBucketEncryption freezes desired and verified native configuration.
// Private snapshots and lease identities are never customer DTOs.
type ObjectBucketEncryption struct {
	BucketID, AccountID, AppID string
	State                      string
	Revision                   int64
	Encryption                 ObjectEncryptionSnapshot `json:"-"`
	DesiredEncryption          ObjectEncryptionSnapshot `json:"-"`
	Token                      string                   `json:"-"`
	LeaseUntil, RetryAt        time.Time                `json:"-"`
	Dispatched                 bool                     `json:"-"`
	UpdatedAt                  time.Time
}

type ObjectBucketEncryptionStore interface {
	GetObjectBucketEncryption(context.Context, string, string, string) (ObjectBucketEncryption, error)
	RequestObjectBucketEncryption(context.Context, string, string, string, ObjectEncryptionSnapshot) (ObjectBucketEncryption, error)
	RefreshObjectBucketEncryption(context.Context, string, string, string, int64) (ObjectBucketEncryption, error)
	DueObjectBucketEncryption(context.Context, int32) ([]ObjectBucketEncryption, error)
	ClaimObjectBucketEncryption(context.Context, string, string) (ObjectBucketEncryption, error)
	DispatchObjectBucketEncryption(context.Context, string, string) (ObjectBucketEncryption, error)
	FinishObjectBucketEncryption(context.Context, string, string, ObjectEncryptionSnapshot) (ObjectBucketEncryption, error)
	RetryObjectBucketEncryption(context.Context, string, string) error
}

func cloneObjectBucketEncryption(j ObjectBucketEncryption) ObjectBucketEncryption {
	j.Encryption, j.DesiredEncryption = j.Encryption.Clone(), j.DesiredEncryption.Clone()
	return j
}

func ViewObjectBucketEncryption(j ObjectBucketEncryption) api.ObjectBucketEncryption {
	out := api.ObjectBucketEncryption{BucketID: j.BucketID, State: j.State, Revision: j.Revision, UpdatedAt: j.UpdatedAt}
	if !j.Encryption.Empty() {
		e := j.Encryption.Clone().Selection
		out.Encryption = &e
	}
	if !j.DesiredEncryption.Empty() {
		e := j.DesiredEncryption.Clone().Selection
		out.DesiredEncryption = &e
	}
	return out
}

func newObjectBucketEncryption(b ObjectBucket, now time.Time) ObjectBucketEncryption {
	return ObjectBucketEncryption{BucketID: b.ID, AccountID: b.AccountID, AppID: b.AppID, State: "ready", UpdatedAt: now, RetryAt: now}
}

func validObjectBucketDefault(e ObjectEncryptionSnapshot, account string) bool {
	return e.ValidFor(account) && e.Selection.Context == ""
}

func requestObjectBucketEncryption(j ObjectBucketEncryption, desired ObjectEncryptionSnapshot, now time.Time) (ObjectBucketEncryption, error) {
	if !validObjectBucketDefault(desired, j.AccountID) {
		return j, ErrConflict
	}
	if j.Revision > 0 && j.DesiredEncryption.Equal(desired) {
		return cloneObjectBucketEncryption(j), nil
	}
	return startObjectBucketEncryption(j, desired, now)
}

func refreshObjectBucketEncryption(j ObjectBucketEncryption, revision int64, now time.Time) (ObjectBucketEncryption, error) {
	if revision < 1 || j.Revision != revision {
		return j, ErrConflict
	}
	return startObjectBucketEncryption(j, j.DesiredEncryption, now)
}

func startObjectBucketEncryption(j ObjectBucketEncryption, desired ObjectEncryptionSnapshot, now time.Time) (ObjectBucketEncryption, error) {
	if j.State != "ready" || j.Revision >= api.MaxObjectBucketEncryptionRevision {
		return j, ErrConflict
	}
	j.Revision++
	j.DesiredEncryption = desired.Clone()
	j.State, j.Token, j.LeaseUntil, j.Dispatched = "waiting", "", time.Time{}, false
	j.RetryAt, j.UpdatedAt = now, now
	return cloneObjectBucketEncryption(j), nil
}

func claimObjectBucketEncryption(j ObjectBucketEncryption, token string, now time.Time) (ObjectBucketEncryption, error) {
	if token == "" || len(token) > api.MaxObjectEncryptionLeaseTokenBytes || j.State == "ready" || j.RetryAt.After(now) || j.LeaseUntil.After(now) {
		return j, ErrConflict
	}
	j.State, j.Token, j.LeaseUntil = "applying", token, now.Add(api.ObjectBucketEncryptionLease)
	j.UpdatedAt = now
	return cloneObjectBucketEncryption(j), nil
}

func validObjectBucketEncryptionLease(j ObjectBucketEncryption, token string, now time.Time) bool {
	return j.State == "applying" && token != "" && j.Token == token && j.LeaseUntil.After(now)
}

func finishObjectBucketEncryption(j ObjectBucketEncryption, token string, verified ObjectEncryptionSnapshot, now time.Time) (ObjectBucketEncryption, error) {
	if !validObjectBucketEncryptionLease(j, token, now) || !j.DesiredEncryption.Equal(verified) {
		return j, ErrConflict
	}
	j.Encryption = verified.Clone()
	j.State, j.Token, j.LeaseUntil = "ready", "", time.Time{}
	j.RetryAt, j.UpdatedAt = now, now
	return cloneObjectBucketEncryption(j), nil
}

func retryObjectBucketEncryption(j ObjectBucketEncryption, token string, now time.Time) (ObjectBucketEncryption, error) {
	if !validObjectBucketEncryptionLease(j, token, now) {
		return j, ErrConflict
	}
	j.State, j.Token, j.LeaseUntil = "waiting", "", time.Time{}
	j.RetryAt, j.UpdatedAt = now.Add(api.ObjectBucketEncryptionRetry), now
	return cloneObjectBucketEncryption(j), nil
}

// A caller selection is explicit. An empty selection captures the verified
// default, and unresolved configuration blocks only new implicit admissions.
func captureObjectBucketDefault(j ObjectBucketEncryption, e ObjectEncryptionSnapshot) (ObjectEncryptionSnapshot, int64, error) {
	if !e.Empty() {
		return e.Clone(), 0, nil
	}
	if j.State != "" && j.State != "ready" {
		return e, 0, ErrConflict
	}
	if j.Encryption.Empty() {
		return e, 0, nil
	}
	return j.Encryption.Clone(), j.Revision, nil
}

func sameMultipartEncryptionRequest(old ObjectMultipartUpload, candidate ObjectEncryptionSnapshot) bool {
	if old.EncryptionDefaultRevision > 0 {
		return candidate.Empty()
	}
	return old.Encryption.Equal(candidate)
}

func capturedDefaultRouteFits(c ObjectUploadCompletion) bool {
	return c.RouteID == "" || c.EncryptionDefaultRevision == 0 ||
		c.RuntimeSinglePutLimit > 0 && c.RuntimeSinglePutLimit <= api.MaxObjectSinglePutBytes && c.Bytes <= c.RuntimeSinglePutLimit
}

func capturedDefaultRouteError(c ObjectUploadCompletion) error {
	return &ObjectStorageLimitError{Kind: "single_put_bytes", Limit: c.RuntimeSinglePutLimit, Observed: c.Bytes, Cause: ErrConflict}
}

func bindObjectURLDefault(c ObjectS3Credential, receipt ObjectUploadCompletion) ObjectS3Credential {
	c = cloneObjectS3Credential(c)
	if !receipt.Encryption.Empty() {
		selection := receipt.Encryption.Clone().Selection
		c.URL.Request.Encryption = &selection
	}
	return c
}

func orderObjectBucketEncryptionJobs(jobs []ObjectBucketEncryption, limit int32) []ObjectBucketEncryption {
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].RetryAt.Before(jobs[j].RetryAt) || jobs[i].RetryAt.Equal(jobs[j].RetryAt) && jobs[i].BucketID < jobs[j].BucketID
	})
	return jobs[:min(len(jobs), int(limit))]
}
