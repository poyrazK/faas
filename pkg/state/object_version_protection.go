package state

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrObjectVersionProtectionPending = errors.New("object version protection is pending")

type ObjectVersionProtection struct {
	api.ObjectVersionProtection
	AccountID, AppID, ProviderVersionID, Token string    `json:"-"`
	LeaseUntil, RetryAt                        time.Time `json:"-"`
	Dispatched                                 bool      `json:"-"`
}

type ObjectVersionProtectionStore interface {
	BeginObjectVersionProtection(context.Context, ObjectVersionProtection) (ObjectVersionProtection, error)
	GetObjectVersionProtection(context.Context, string, string, string) (ObjectVersionProtection, error)
	DueObjectVersionProtection(context.Context, int32) ([]ObjectVersionProtection, error)
	ClaimObjectVersionProtection(context.Context, string, string) (ObjectVersionProtection, error)
	DispatchObjectVersionProtection(context.Context, string, string) (ObjectVersionProtection, error)
	FinishObjectVersionProtection(context.Context, string, string, string, string) (ObjectVersionProtection, error)
	RetryObjectVersionProtection(context.Context, string, string, string) error
}

func cloneObjectVersionProtection(j ObjectVersionProtection) ObjectVersionProtection {
	if j.Retention != nil {
		r := j.Retention.Clone()
		j.Retention = &r
	}
	if j.LegalHold != nil {
		h := *j.LegalHold
		j.LegalHold = &h
	}
	return j
}
func ViewObjectVersionProtection(j ObjectVersionProtection) api.ObjectVersionProtection {
	return cloneObjectVersionProtection(j).ObjectVersionProtection
}
func protectionActive(j ObjectVersionProtection) bool {
	return j.State == "waiting" || j.State == "applying"
}
func ValidObjectVersionProtectionIntent(j ObjectVersionProtection) bool {
	u, e := uuid.Parse(j.ID)
	if e != nil || u.String() != j.ID || u.Version() != 4 || u.Variant() != uuid.RFC4122 || !ValidObjectVersionID(j.VersionID) || !validVersionReferenceText(j.Key, api.MaxObjectS3ListTextBytes) {
		return false
	}
	switch j.Kind {
	case "retention":
		return j.LegalHold == nil && j.Retention != nil && j.Retention.ValidForWrite() && j.Retention.EventHold == "" && j.Retention.EventHoldDuration == nil
	case "legal_hold":
		return j.Retention == nil && j.LegalHold != nil && j.LegalHold.Valid()
	}
	return false
}
func sameProtectionIntent(a, b ObjectVersionProtection) bool {
	return a.AccountID == b.AccountID && a.AppID == b.AppID && a.BucketID == b.BucketID && a.Key == b.Key && a.VersionID == b.VersionID && a.Kind == b.Kind && reflect.DeepEqual(a.Retention, b.Retention) && reflect.DeepEqual(a.LegalHold, b.LegalHold)
}
func newProtectionIntent(j ObjectVersionProtection, native string, now time.Time) ObjectVersionProtection {
	j = cloneObjectVersionProtection(j)
	j.ProviderVersionID = native
	j.State = "waiting"
	j.Token = ""
	j.Dispatched = false
	j.LeaseUntil = time.Time{}
	j.LastErrorCode = ""
	j.CreatedAt = now
	j.UpdatedAt = now
	j.RetryAt = now
	return j
}
func claimProtection(j ObjectVersionProtection, token string, now time.Time) (ObjectVersionProtection, error) {
	if !protectionActive(j) || token == "" || len(token) > api.MaxObjectLockLeaseTokenBytes || j.RetryAt.After(now) || j.LeaseUntil.After(now) {
		return j, ErrConflict
	}
	j.State = "applying"
	j.Token = token
	j.LeaseUntil = now.Add(api.ObjectVersionProtectionLease)
	j.UpdatedAt = now
	return cloneObjectVersionProtection(j), nil
}
func validProtectionLease(j ObjectVersionProtection, token string, now time.Time) bool {
	return j.State == "applying" && token != "" && j.Token == token && j.LeaseUntil.After(now)
}
func finishProtection(j ObjectVersionProtection, token, status, code string, now time.Time) (ObjectVersionProtection, error) {
	if !validProtectionLease(j, token, now) || status != "ready" && status != "failed" || status == "ready" && code != "" || status == "failed" && code != "preparation_failed" && code != "provider_rejected" {
		return j, ErrConflict
	}
	if status == "failed" && j.Dispatched && code != "provider_rejected" {
		return j, ErrConflict
	}
	j.State = status
	j.LastErrorCode = code
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	return cloneObjectVersionProtection(j), nil
}
func retryProtection(j ObjectVersionProtection, token, code string, now time.Time) (ObjectVersionProtection, error) {
	if !validProtectionLease(j, token, now) || code != "provider_uncertain" && code != "provider_unsupported" && code != "provider_mismatch" {
		return j, ErrConflict
	}
	j.State = "waiting"
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.RetryAt = now.Add(api.ObjectVersionProtectionRetry)
	j.UpdatedAt = now
	j.LastErrorCode = code
	return cloneObjectVersionProtection(j), nil
}
