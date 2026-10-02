package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MutableObjectDeleter issues one ordinary or null-selector DELETE. A successful
// response is a receipt, never an instruction to refund capacity. Only
// ErrDeletionRejected proves that the dispatched attempt did not mutate.
type MutableObjectDeleter interface {
	DeleteMutableObject(context.Context, string, string, string) (MutableDeleteResult, error)
}
type MutableDeleteResult struct {
	ProviderVersionID string
	DeleteMarker      bool
}
type DeletionService struct {
	Store         state.ObjectDeletionStore
	Provider      Provider
	BeforeRequest func(context.Context) error
}

func (s DeletionService) before(ctx context.Context) error {
	if s.BeforeRequest != nil {
		return s.BeforeRequest(ctx)
	}
	return nil
}
func (s DeletionService) Start(ctx context.Context, b state.ObjectBucket, key, selector, id string, p api.ObjectStoragePolicy) (state.ObjectDeletion, error) {
	if !ValidKey(key) || selector != "" && !state.ValidObjectVersionID(selector) {
		return state.ObjectDeletion{}, ErrInvalid
	}
	if v, e := uuid.Parse(id); e != nil || v.String() != id {
		return state.ObjectDeletion{}, ErrInvalid
	}
	if s.Store == nil || s.Provider == nil {
		return state.ObjectDeletion{}, ErrUnsupported
	}
	if selector == "null" {
		if _, ok := s.Provider.(MutableObjectDeleter); !ok {
			return state.ObjectDeletion{}, ErrUnsupported
		}
	}
	if selector != "" && selector != "null" {
		if _, ok := s.Provider.(ObjectVersionDeleter); !ok {
			return state.ObjectDeletion{}, ErrUnsupported
		}
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectDeletionOperationTimeout)
	defer cancel()
	j, created, e := s.Store.BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: id, BucketID: b.ID, Key: key, Selector: selector}, AccountID: b.AccountID, AppID: b.AppID, Token: uuid.NewString()}, p)
	if e != nil || !created {
		return j, e
	}
	baseline, status, e := s.prepare(ctx, b, j)
	if e != nil {
		return s.failPreparation(ctx, j, e)
	}
	// Metrics must commit before dispatch; a recording failure cannot mutate.
	if e = s.before(ctx); e != nil {
		return s.failPreparation(ctx, j, e)
	}
	j, e = s.Store.DispatchObjectDeletion(ctx, j.ID, j.Token, status, baseline)
	if e != nil {
		return j, e
	}
	return s.execute(ctx, b, j, false)
}

func (s DeletionService) execute(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion, recovery bool) (state.ObjectDeletion, error) {
	var receipt MutableDeleteResult
	var e error
	if j.TargetProviderVersionID != "" {
		provider, ok := s.Provider.(ObjectVersionDeleter)
		if !ok || !validNativeVersionID(j.TargetProviderVersionID) || j.TargetProviderVersionID == "null" {
			return s.deferAttempt(ctx, j, ErrUnsupported)
		}
		var out VersionDeleteResult
		out, e = provider.DeleteObjectVersion(ctx, b.PhysicalName, j.Key, j.TargetProviderVersionID)
		receipt = MutableDeleteResult{ProviderVersionID: j.TargetProviderVersionID, DeleteMarker: out.DeleteMarker}
	} else if provider, ok := s.Provider.(MutableObjectDeleter); ok {
		receipt, e = provider.DeleteMutableObject(ctx, b.PhysicalName, j.Key, j.Selector)
	} else {
		e = s.Provider.DeleteObject(ctx, b.PhysicalName, j.Key)
	}
	if e == nil && !validMutableDeleteReceipt(j, receipt) {
		e = ErrUnavailable
	}
	if e != nil {
		// Rejection of a recovery request says nothing about the original
		// dispatched attempt, which could still be executing at the provider.
		if errors.Is(e, ErrDeletionRejected) && !recovery {
			return s.fail(ctx, j, "provider_rejected", e)
		}
		return s.deferAttempt(ctx, j, e)
	}
	return s.finish(ctx, j, receipt)
}

func validMutableDeleteReceipt(j state.ObjectDeletion, r MutableDeleteResult) bool {
	if j.TargetProviderVersionID != "" {
		return r.ProviderVersionID == j.TargetProviderVersionID
	}
	if j.Selector == "null" {
		return r.ProviderVersionID == "null"
	}
	switch j.ProviderStatus {
	case "Enabled":
		return r.DeleteMarker && validNativeVersionID(r.ProviderVersionID) && r.ProviderVersionID != "null" && !slices.Contains(j.Baseline, deletionVersionHash(r.ProviderVersionID))
	case "Suspended":
		return r.DeleteMarker && r.ProviderVersionID == "null"
	case "":
		return !r.DeleteMarker && r.ProviderVersionID == ""
	default:
		return false
	}
}
func (s DeletionService) prepare(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) ([]string, string, error) {
	if j.Selector != "" && j.Selector != "null" {
		if !validNativeVersionID(j.TargetProviderVersionID) || j.TargetProviderVersionID == "null" {
			return nil, "", ErrUnavailable
		}
		return []string{}, "", nil
	}
	status := j.ProviderStatus
	if p, ok := s.Provider.(BucketVersioningProvider); ok {
		if e := s.before(ctx); e != nil {
			return nil, status, e
		}
		v, e := p.GetBucketVersioning(ctx, b.PhysicalName)
		if e != nil {
			return nil, status, e
		}
		if v.MFADelete == "Enabled" {
			return nil, status, ErrUnsupported
		}
		if v.Status != status {
			return nil, status, ErrConflict
		}
	}
	if status != "" {
		if _, ok := s.Provider.(MutableObjectDeleter); !ok {
			return nil, status, ErrUnsupported
		}
	}
	if status != "Enabled" || j.Selector != "" {
		return []string{}, status, nil
	}
	// Every owned deletion is fenced by this intent, so continuation identities
	// cannot disappear while preparation binds the complete prior history.
	entries, e := s.history(ctx, b, j.Key, api.ObjectDeletionHistoryPages)
	if e != nil {
		return nil, status, e
	}
	baseline := []string{}
	for _, v := range entries {
		baseline = append(baseline, deletionVersionHash(v.ProviderVersionID))
	}
	return baseline, status, nil
}
func deletionVersionHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
func (s DeletionService) history(ctx context.Context, b state.ObjectBucket, key string, pages int) ([]ListedObjectVersion, error) {
	p, ok := s.Provider.(ObjectVersionLister)
	if !ok {
		return nil, ErrUnsupported
	}
	request := ObjectVersionListRequest{Prefix: key, Limit: api.MaxObjectS3ListItems}
	out := []ListedObjectVersion{}
	identities := map[string]bool{}
	cursors := map[string]bool{}
	for range pages {
		if e := s.before(ctx); e != nil {
			return nil, e
		}
		page, e := p.ListObjectVersionPage(ctx, b.PhysicalName, request)
		if e != nil {
			return nil, e
		}
		if len(page.Items) > api.MaxObjectS3ListItems || len(page.CommonPrefixes) != 0 {
			return nil, ErrUnavailable
		}
		for _, v := range page.Items {
			if !ValidKey(v.Key) || !validNativeVersionID(v.ProviderVersionID) {
				return nil, ErrUnavailable
			}
			if v.Key != key {
				continue
			}
			hash := deletionVersionHash(v.ProviderVersionID)
			if identities[hash] {
				return nil, ErrUnavailable
			}
			identities[hash] = true
			out = append(out, v)
			if len(out) > api.ObjectDeletionHistoryVersions {
				return nil, ErrUnavailable
			}
		}
		if page.NextKeyMarker == "" || page.NextKeyMarker > key {
			return out, nil
		}
		cursor := page.NextKeyMarker + "\x00" + page.NextProviderVersionMarker
		if cursors[cursor] || page.NextKeyMarker != key || page.NextProviderVersionMarker == "" {
			return nil, ErrUnavailable
		}
		cursors[cursor] = true
		request.KeyMarker = page.NextKeyMarker
		request.ProviderVersionMarker = page.NextProviderVersionMarker
	}
	return nil, ErrUnavailable
}
func (s DeletionService) finish(ctx context.Context, j state.ObjectDeletion, r MutableDeleteResult) (state.ObjectDeletion, error) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectDeletionRetry)
	defer cancel()
	j.State = "completed"
	j.LastErrorCode = ""
	j.ProviderVersionID = r.ProviderVersionID
	j.DeleteMarker = r.DeleteMarker
	if j.Selector != "" {
		j.VersionID = j.Selector
	}
	return s.Store.FinishObjectDeletion(finish, j)
}
func (s DeletionService) failPreparation(ctx context.Context, j state.ObjectDeletion, cause error) (state.ObjectDeletion, error) {
	return s.fail(ctx, j, "preparation_failed", cause)
}
func (s DeletionService) fail(ctx context.Context, j state.ObjectDeletion, code string, cause error) (state.ObjectDeletion, error) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectDeletionRetry)
	defer cancel()
	j.State = "failed"
	j.LastErrorCode = code
	out, e := s.Store.FinishObjectDeletion(finish, j)
	if e != nil {
		return out, e
	}
	return out, cause
}
func (s DeletionService) deferAttempt(ctx context.Context, j state.ObjectDeletion, cause error) (state.ObjectDeletion, error) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectDeletionRetry)
	defer cancel()
	code := "provider_uncertain"
	if errors.Is(cause, ErrConfiguration) {
		code = "configuration"
	}
	if e := s.Store.RetryObjectDeletion(finish, j.ID, j.Token, code); e != nil {
		return j, e
	}
	out, e := s.Store.GetObjectDeletion(finish, j.AccountID, j.BucketID, j.ID)
	if e != nil {
		return j, e
	}
	return out, cause
}

// Recovery may re-dispatch only an immutable selected version. Mutable
// deletion requires a unique marker proof; absence and expiry are insufficient.
func (s DeletionService) Recover(ctx context.Context, b state.ObjectBucket, id string) (state.ObjectDeletion, error) {
	if s.Store == nil {
		return state.ObjectDeletion{}, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectDeletionOperationTimeout)
	defer cancel()
	if _, e := s.Store.GetObjectDeletion(ctx, b.AccountID, b.ID, id); e != nil {
		return state.ObjectDeletion{}, e
	}
	j, e := s.Store.ClaimObjectDeletion(ctx, id, uuid.NewString())
	if e != nil {
		return j, e
	}
	if j.State == "prepared" {
		j.State = "failed"
		j.LastErrorCode = "preparation_expired"
		return s.Store.FinishObjectDeletion(ctx, j)
	}
	if s.Provider == nil {
		return s.deferAttempt(ctx, j, ErrConfiguration)
	}
	if j.TargetProviderVersionID != "" {
		if e = s.before(ctx); e != nil {
			return s.deferAttempt(ctx, j, e)
		}
		return s.execute(ctx, b, j, true)
	}
	if j.Selector != "" || j.ProviderStatus != "Enabled" {
		return s.deferAttempt(ctx, j, ErrUnavailable)
	}
	versions, e := s.history(ctx, b, j.Key, api.ObjectDeletionHistoryPages)
	if e != nil {
		return s.deferAttempt(ctx, j, e)
	}
	var proof *ListedObjectVersion
	for i := range versions {
		v := &versions[i]
		if v.DeleteMarker && v.ProviderVersionID != "null" && !slices.Contains(j.Baseline, deletionVersionHash(v.ProviderVersionID)) {
			if proof != nil {
				return s.deferAttempt(ctx, j, ErrUnavailable)
			}
			proof = v
		}
	}
	if proof == nil {
		return s.deferAttempt(ctx, j, ErrUnavailable)
	}
	return s.finish(ctx, j, MutableDeleteResult{ProviderVersionID: proof.ProviderVersionID, DeleteMarker: true})
}
