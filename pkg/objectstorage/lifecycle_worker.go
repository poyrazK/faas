package objectstorage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type LifecycleExpirationStore interface {
	state.ObjectLifecycleStore
	state.ObjectDeletionStore
	state.ObjectDeletionActivityStore
	state.ObjectVersionReferenceStore
}

type LifecycleExpirationService struct {
	Store           LifecycleExpirationStore
	Provider        Provider
	BeforeRequest   func(context.Context) error
	BeforeAdmission func(context.Context) error
}

var errLifecycleActionsPending = errors.New("lifecycle key has more actions than one step permits")

// Step claims one durable scan and processes at most one complete key. Its only
// durable discovery cursor is a key, never a native version which it may delete.
// Failed steps release the claim with a bounded delay; deletion recovery owns
// any prepared or dispatched receipt left by an interrupted process.
func (s LifecycleExpirationService) Step(ctx context.Context, b state.ObjectBucket, p api.ObjectStoragePolicy) (j state.ObjectLifecycleScan, err error) {
	if s.Store == nil {
		return j, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectLifecycleWorkerTimeout)
	defer cancel()
	j, err = s.Store.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		return j, err
	}
	j, err = s.Store.ClaimObjectLifecycleScan(ctx, j.ID, uuid.NewString())
	if err != nil {
		return j, err
	}
	claimedID, claimedToken := j.ID, j.Token
	defer func() {
		if err != nil {
			finish, stop := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectLifecycleFinishTimeout)
			defer stop()
			if retryErr := s.Store.RetryObjectLifecycleScan(finish, claimedID, claimedToken); retryErr != nil && !errors.Is(retryErr, state.ErrConflict) {
				err = errors.Join(err, retryErr)
			}
		}
	}()
	active, err := s.Store.HasActiveObjectDeletion(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		return j, err
	}
	if active {
		return j, state.ErrConflict
	}
	if j.Phase == "multipart" {
		return s.multipartStep(ctx, j)
	}
	// An abort-only scan created before the phase migration still starts in
	// objects. Advance it without requiring provider version-list support.
	if !lifecycleScanHasObjectActions(j.Rules) {
		return s.Store.CheckpointObjectLifecycleScan(ctx, j.ID, j.Token, j.LastKey, true)
	}
	deletion := DeletionService{Store: s.Store, Provider: s.Provider, BeforeRequest: s.BeforeRequest}
	key, err := lifecycleNextKey(ctx, deletion, b, j.LastKey)
	if err != nil {
		return j, err
	}
	if key == "" {
		return s.Store.CheckpointObjectLifecycleScan(ctx, j.ID, j.Token, j.LastKey, true)
	}
	versions, err := deletion.history(ctx, b, key, api.ObjectDeletionHistoryPages)
	if err != nil {
		return j, err
	}
	if err = s.expireKey(ctx, deletion, b, j, versions, p); err != nil {
		return j, err
	}
	return s.Store.CheckpointObjectLifecycleScan(ctx, j.ID, j.Token, key, false)
}

func lifecycleScanHasObjectActions(rules []api.ObjectLifecycleRule) bool {
	for _, r := range rules {
		if r.Status == "Enabled" && (r.Expiration != nil || r.NoncurrentVersionExpiration != nil) {
			return true
		}
	}
	return false
}

func (s LifecycleExpirationService) multipartStep(ctx context.Context, j state.ObjectLifecycleScan) (state.ObjectLifecycleScan, error) {
	rows, err := s.Store.ListObjectLifecycleMultipartUploads(ctx, j.ID, j.Token)
	if err != nil {
		return j, err
	}
	if s.BeforeAdmission != nil {
		if err = s.BeforeAdmission(ctx); err != nil {
			return j, err
		}
	}
	var candidate state.ObjectMultipartUpload
	if len(rows) > 0 {
		candidate = rows[0]
	}
	return s.Store.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, candidate)
}

func lifecycleNextKey(ctx context.Context, s DeletionService, b state.ObjectBucket, last string) (string, error) {
	lister, ok := s.Provider.(ObjectVersionLister)
	if !ok {
		return "", ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return "", err
	}
	page, err := lister.ListObjectVersionPage(ctx, b.PhysicalName, ObjectVersionListRequest{KeyMarker: last, Limit: api.ObjectLifecycleDiscoveryPageSize})
	if err != nil {
		return "", err
	}
	if len(page.Items) > int(api.ObjectLifecycleDiscoveryPageSize) || len(page.CommonPrefixes) != 0 || page.NextKeyMarker == "" && page.NextProviderVersionMarker != "" || len(page.Items) == 0 && page.NextKeyMarker != "" {
		return "", ErrUnavailable
	}
	if len(page.Items) == 0 {
		return "", nil
	}
	key := page.Items[0].Key
	if !ValidKey(key) || key <= last || page.NextKeyMarker != "" && page.NextKeyMarker < key {
		return "", ErrUnavailable
	}
	return key, nil
}

func (s LifecycleExpirationService) expireKey(ctx context.Context, deletion DeletionService, b state.ObjectBucket, scan state.ObjectLifecycleScan, versions []ListedObjectVersion, p api.ObjectStoragePolicy) error {
	rules, err := api.NormalizeObjectLifecycleRules(scan.Rules)
	if err != nil {
		return ErrInvalid
	}
	actions := 0
	for _, target := range versions {
		o, err := lifecycleHistoryObject(versions, target.ProviderVersionID, target.LastModified, time.Now().UTC())
		if err != nil {
			return err
		}
		for _, rule := range rules {
			if err = ctx.Err(); err != nil {
				return err
			}
			// Discovery only plans a possible age/prefix action. Actual tags
			// and complete history are read again under the deletion fence.
			o.Tags = rule.Filter.Tags
			decision, err := SelectLifecycleAction([]api.ObjectLifecycleRule{rule}, o, time.Now().UTC())
			if err != nil {
				return err
			}
			if decision.Kind == "" {
				continue
			}
			selector, err := s.selector(ctx, b, target, decision)
			if err != nil {
				return err
			}
			id, err := lifecycleDeletionReceiptID(scan.ID, target, selector, decision)
			if err != nil {
				return err
			}
			old, err := s.Store.GetObjectDeletion(ctx, b.AccountID, b.ID, id)
			if err == nil {
				if old.State == "completed" {
					break
				}
				if old.State == "failed" {
					continue
				}
				return state.ErrConflict
			}
			if !errors.Is(err, state.ErrNotFound) {
				return err
			}
			if actions >= api.ObjectLifecycleActionsPerStep {
				return errLifecycleActionsPending
			}
			actions++
			j, err := deletion.StartLifecycle(ctx, b, scan, target, selector, decision, p)
			if err != nil {
				if j.State == "failed" && errors.Is(err, ErrLifecycleNotDue) {
					continue
				}
				return err
			}
			if j.State != "completed" {
				return state.ErrConflict
			}
			break
		}
	}
	return nil
}

func (s LifecycleExpirationService) selector(ctx context.Context, b state.ObjectBucket, target ListedObjectVersion, decision LifecycleDecision) (string, error) {
	if decision.Kind == "current" {
		return "", nil
	}
	if target.ProviderVersionID == "null" {
		return "null", nil
	}
	refs, err := s.Store.RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: target.Key, ProviderVersionID: target.ProviderVersionID, DeleteMarker: target.DeleteMarker}})
	if err != nil {
		return "", err
	}
	if len(refs) != 1 || !state.ValidObjectVersionID(refs[0].ID) || refs[0].ID == "null" {
		return "", ErrUnavailable
	}
	return refs[0].ID, nil
}
