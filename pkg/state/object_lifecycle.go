package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Discovery is durable separately from mutation. A completed scan is not proof
// of expiration or capacity reclamation; actions must use the deletion journal.
type ObjectLifecycleStore interface {
	SetObjectBucketLifecycle(context.Context, string, string, string, []api.ObjectLifecycleRule) (ObjectLifecyclePolicy, error)
	GetObjectBucketLifecycle(context.Context, string, string, string) (ObjectLifecyclePolicy, error)
	DueObjectLifecyclePolicies(context.Context, int32) ([]ObjectLifecyclePolicy, error)
	StartObjectLifecycleScan(context.Context, string, string, string) (ObjectLifecycleScan, error)
	GetObjectLifecycleScan(context.Context, string, string, string) (ObjectLifecycleScan, error)
	ClaimObjectLifecycleScan(context.Context, string, string) (ObjectLifecycleScan, error)
	CheckpointObjectLifecycleScan(context.Context, string, string, string, bool) (ObjectLifecycleScan, error)
	RetryObjectLifecycleScan(context.Context, string, string) error
}

type ObjectLifecyclePolicy struct {
	api.ObjectBucketLifecycle
	AccountID, AppID string    `json:"-"`
	NextScanAt       time.Time `json:"-"`
}

type ObjectLifecycleScan struct {
	api.ObjectLifecycleScan
	AccountID, AppID, Token, LastKey string                    `json:"-"`
	Rules                            []api.ObjectLifecycleRule `json:"-"`
	LeaseUntil, RetryAt              time.Time                 `json:"-"`
}

func cloneLifecyclePolicy(p ObjectLifecyclePolicy) ObjectLifecyclePolicy {
	p.Rules = api.CloneObjectLifecycleRules(p.Rules)
	return p
}
func cloneLifecycleScan(j ObjectLifecycleScan) ObjectLifecycleScan {
	j.Rules = api.CloneObjectLifecycleRules(j.Rules)
	if j.FinishedAt != nil {
		v := *j.FinishedAt
		j.FinishedAt = &v
	}
	return j
}
func lifecycleRulesEqual(a, b []api.ObjectLifecycleRule) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && string(x) == string(y)
}
func lifecycleEnabled(p ObjectLifecyclePolicy) bool {
	for _, r := range p.Rules {
		if r.Status == "Enabled" {
			return true
		}
	}
	return false
}
func newLifecyclePolicy(b ObjectBucket, now time.Time) ObjectLifecyclePolicy {
	return ObjectLifecyclePolicy{ObjectBucketLifecycle: api.ObjectBucketLifecycle{BucketID: b.ID, Rules: []api.ObjectLifecycleRule{}, UpdatedAt: now}, AccountID: b.AccountID, AppID: b.AppID, NextScanAt: now}
}
func newLifecycleScan(p ObjectLifecyclePolicy, now time.Time) ObjectLifecycleScan {
	return ObjectLifecycleScan{ObjectLifecycleScan: api.ObjectLifecycleScan{ID: uuid.NewString(), BucketID: p.BucketID, Revision: p.Revision, State: "scanning", CreatedAt: now, UpdatedAt: now}, AccountID: p.AccountID, AppID: p.AppID, Rules: api.CloneObjectLifecycleRules(p.Rules), RetryAt: now}
}
func validLifecycleScanLease(j ObjectLifecycleScan, token string, now time.Time) bool {
	return j.State == "scanning" && token != "" && j.Token == token && j.LeaseUntil.After(now)
}
func claimLifecycleScan(j ObjectLifecycleScan, token string, now time.Time) (ObjectLifecycleScan, error) {
	if token == "" || !validVersionReferenceText(token, api.MaxObjectLifecycleTokenBytes) || j.State != "scanning" || j.RetryAt.After(now) || j.LeaseUntil.After(now) {
		return j, ErrConflict
	}
	j.Token, j.LeaseUntil, j.UpdatedAt = token, now.Add(api.ObjectLifecycleLease), now
	return j, nil
}
func checkpointLifecycleScan(j ObjectLifecycleScan, token, key string, done bool, now time.Time) (ObjectLifecycleScan, error) {
	if !validLifecycleScanLease(j, token, now) || done && key != j.LastKey || !done && (!validVersionReferenceText(key, api.MaxObjectS3ListTextBytes) || key <= j.LastKey || j.ScannedKeys >= api.MaxObjectStoragePolicyValue) {
		return j, ErrConflict
	}
	if done {
		j.State, j.FinishedAt = "completed", &now
	} else {
		j.LastKey = key
		j.ScannedKeys++
	}
	j.Token, j.LeaseUntil, j.RetryAt, j.UpdatedAt = "", time.Time{}, now, now
	return j, nil
}
