package objectstorage

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrLifecycleNotDue = errors.New("object no longer qualifies for lifecycle expiration")

// StartLifecycle revalidates an immutable discovery identity under the ordinary
// deletion fence. The scan lease and rule revision are checked atomically by
// the store at both journal creation and dispatch. Replays never redispatch.
func (s DeletionService) StartLifecycle(ctx context.Context, b state.ObjectBucket, scan state.ObjectLifecycleScan, target ListedObjectVersion, selector string, decision LifecycleDecision, p api.ObjectStoragePolicy) (state.ObjectDeletion, error) {
	if _, ok := s.Provider.(ObjectVersionLister); !ok {
		return state.ObjectDeletion{}, ErrUnsupported
	}
	if scan.AccountID != b.AccountID || scan.AppID != b.AppID || scan.BucketID != b.ID || !ValidKey(target.Key) || !validNativeVersionID(target.ProviderVersionID) || target.LastModified.IsZero() {
		return state.ObjectDeletion{}, ErrInvalid
	}
	namespace, err := uuid.Parse(scan.ID)
	if err != nil || namespace.Version() != 4 || namespace.String() != scan.ID {
		return state.ObjectDeletion{}, ErrInvalid
	}
	var rule *api.ObjectLifecycleRule
	normal, err := api.NormalizeObjectLifecycleRules(scan.Rules)
	if err != nil {
		return state.ObjectDeletion{}, ErrInvalid
	}
	for i := range normal {
		if normal[i].ID == decision.RuleID {
			rule = &normal[i]
			break
		}
	}
	if rule == nil {
		return state.ObjectDeletion{}, ErrInvalid
	}
	binding := &state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: rule.ID, Kind: decision.Kind, ExpectedProviderVersionID: target.ProviderVersionID, ExpectedLastModified: target.LastModified.UTC()}
	identity := target.Key + "\x00" + selector + "\x00" + rule.ID + "\x00" + decision.Kind + "\x00" + target.ProviderVersionID + "\x00" + target.LastModified.UTC().Format(time.RFC3339Nano)
	id := uuid.NewSHA1(namespace, []byte(identity)).String()
	previous := s.Preflight
	s.Preflight = func(ctx context.Context, bucket state.ObjectBucket, j state.ObjectDeletion) error {
		if previous != nil {
			if err := previous(ctx, bucket, j); err != nil {
				return err
			}
		}
		return s.lifecyclePreflight(ctx, bucket, j, *rule)
	}
	return s.start(ctx, b, target.Key, selector, id, binding, p)
}

func (s DeletionService) lifecyclePreflight(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion, rule api.ObjectLifecycleRule) error {
	if j.Lifecycle == nil {
		return ErrInvalid
	}
	versions, err := s.history(ctx, b, j.Key, api.ObjectDeletionHistoryPages)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	o, err := lifecycleHistoryObject(versions, j.Lifecycle.ExpectedProviderVersionID, j.Lifecycle.ExpectedLastModified, now)
	if err != nil {
		return err
	}
	if len(rule.Filter.Tags) != 0 && !o.DeleteMarker {
		o.Tags, err = s.lifecycleTags(ctx, b, j)
		if err != nil {
			return err
		}
	}
	decision, err := SelectLifecycleAction([]api.ObjectLifecycleRule{rule}, o, now)
	if err != nil {
		return err
	}
	if decision.Kind != j.Lifecycle.Kind || decision.RuleID != j.Lifecycle.RuleID {
		return ErrLifecycleNotDue
	}
	return nil
}

func (s DeletionService) lifecycleTags(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) (map[string]string, error) {
	if err := s.before(ctx); err != nil {
		return nil, err
	}
	native := j.Lifecycle.ExpectedProviderVersionID
	unversioned := j.ProviderStatus == "" && j.Selector == "" && native == "null"
	if p, ok := s.Provider.(ObjectVersionTagger); ok {
		selector := native
		if unversioned {
			selector = ""
		}
		result, err := p.GetObjectVersionTags(ctx, b.PhysicalName, j.Key, selector)
		if err != nil {
			return nil, err
		}
		if result.ProviderVersionID != native && (!unversioned || result.ProviderVersionID != "") {
			return nil, ErrUnavailable
		}
		if ValidateObjectMetadata(ObjectMetadata{Tags: result.Tags}) != nil {
			return nil, ErrUnavailable
		}
		return result.Tags, nil
	}
	if p, ok := s.Provider.(ObjectTagger); ok && unversioned {
		tags, err := p.GetObjectTags(ctx, b.PhysicalName, j.Key)
		if err != nil {
			return nil, err
		}
		if ValidateObjectMetadata(ObjectMetadata{Tags: tags}) != nil {
			return nil, ErrUnavailable
		}
		return tags, nil
	}
	return nil, ErrUnsupported
}

// lifecycleHistoryObject takes a complete exact-key history. Equal timestamps
// have no reliable cross-type order, so only strictly newer noncurrent versions
// count toward retention. The closest strictly newer timestamp is a conservative
// successor bound; ties are never used to expire a version prematurely.
func lifecycleHistoryObject(versions []ListedObjectVersion, native string, modified, now time.Time) (LifecycleObject, error) {
	if len(versions) > api.ObjectDeletionHistoryVersions {
		return LifecycleObject{}, ErrUnavailable
	}
	latest := -1
	seen := map[string]bool{}
	for i, v := range versions {
		if !ValidKey(v.Key) || !validNativeVersionID(v.ProviderVersionID) || seen[v.ProviderVersionID] || v.LastModified.IsZero() || v.LastModified.After(now) || i > 0 && v.Key != versions[0].Key {
			return LifecycleObject{}, ErrUnavailable
		}
		seen[v.ProviderVersionID] = true
		if v.IsLatest {
			if latest >= 0 {
				return LifecycleObject{}, ErrUnavailable
			}
			latest = i
		}
	}
	if len(versions) == 0 {
		return LifecycleObject{}, ErrLifecycleNotDue
	}
	if latest < 0 {
		return LifecycleObject{}, ErrUnavailable
	}
	current := versions[latest]
	for _, v := range versions {
		if v.LastModified.After(current.LastModified) {
			return LifecycleObject{}, ErrUnavailable
		}
	}
	index := slices.IndexFunc(versions, func(v ListedObjectVersion) bool {
		return v.ProviderVersionID == native && v.LastModified.Equal(modified)
	})
	if index < 0 {
		return LifecycleObject{}, ErrLifecycleNotDue
	}
	v := versions[index]
	o := LifecycleObject{Key: v.Key, LastModified: v.LastModified, IsLatest: v.IsLatest, DeleteMarker: v.DeleteMarker, OnlyVersion: len(versions) == 1}
	if !v.IsLatest {
		o.NoncurrentSince = current.LastModified
		for _, newer := range versions {
			if !newer.LastModified.After(v.LastModified) {
				continue
			}
			if newer.LastModified.Before(o.NoncurrentSince) {
				o.NoncurrentSince = newer.LastModified
			}
			if !newer.IsLatest {
				o.NewerNoncurrent++
			}
		}
	}
	return o, nil
}
