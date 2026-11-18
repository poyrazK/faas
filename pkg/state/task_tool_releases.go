package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrTaskToolReleaseLimit = errors.New("task release intent journal is full")

// TaskToolReleaseIntentStore is the apid-owned review journal. It has no apply,
// deployment, migration, credential retrieval, or registry publication method.
type TaskToolReleaseIntentStore interface {
	PrepareTaskToolReleaseIntent(context.Context, string, string, string, api.TaskToolReleaseIntent) (api.TaskToolReleaseIntentRecord, error)
	GetTaskToolReleaseIntent(context.Context, string, string) (api.TaskToolReleaseIntentRecord, error)
	ChangeTaskToolReleaseIntent(context.Context, string, string, string, api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error)
}

func cloneTaskToolRelease(in api.TaskToolReleaseIntentRecord) api.TaskToolReleaseIntentRecord {
	body, _ := json.Marshal(in)
	var out api.TaskToolReleaseIntentRecord
	_ = json.Unmarshal(body, &out)
	return out
}

func changeTaskToolRelease(record api.TaskToolReleaseIntentRecord, action string, in api.TaskToolReleaseIntentDecision, fresh bool) (api.TaskToolReleaseIntentRecord, error) {
	if action == "decide" && tasktoolrelease.IsRecoveryDecision(in.Decision) {
		out, err := tasktoolrelease.Recovery(record, in, fresh, time.Now())
		if err != nil {
			return record, ErrConflict
		}
		return out, nil
	}
	if action == "execution" || action == "decide" && in.Decision == "authorize" {
		out, err := tasktoolrelease.Execution(record, in, fresh, time.Now())
		if err != nil {
			return record, ErrConflict
		}
		return out, nil
	}
	if (action != "check" && action != "decide") || !toolpublication.ValidID(in.ReleaseFingerprint) {
		return record, ErrInvalidArgument
	}
	if action == "decide" && in.Decision != "approve" && in.Decision != "reject" && in.Decision != "revoke" {
		return record, ErrInvalidArgument
	}
	if action != "check" && (in.IntentSHA256 != record.IntentSHA256 || in.ReleaseFingerprint != record.Intent.ReleaseFingerprint) {
		return record, ErrConflict
	}
	if action == "check" || in.Decision == "approve" {
		record = tasktoolrelease.Check(record, in.ReleaseFingerprint, fresh, time.Now())
	} else {
		// Cancelling the exact retained review does not certify current app,
		// source or requester authority, and must remain possible after drift.
		record = tasktoolrelease.View(record, time.Now())
	}
	if action == "check" || record.Status == "stale" || record.Status == "expired" {
		return record, nil
	}
	out, err := tasktoolrelease.Decide(record, in, time.Now())
	if err != nil {
		return record, ErrConflict
	}
	return out, nil
}

func (m *MemStore) taskToolReleaseKeyFreshLocked(account, key string) bool {
	k, ok := m.keys[key]
	_, limited := m.keyProjectLimits[key]
	if !ok || k.AccountID != account || limited || k.Status != "active" && k.Status != "grace" || k.RevokedAt != nil ||
		k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt) || k.AppID != "" || k.PlatformTenantID != "" || k.HostingSiteID != "" ||
		!slices.Contains(k.Scopes, api.ScopeAdmin) && !slices.Contains(k.Scopes, api.ScopeDeployWrite) {
		return false
	}
	if k.OrgID != "" {
		org, ok := m.orgs[k.OrgID]
		return ok && org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == account
	}
	return true
}

func (m *MemStore) taskToolReleaseFreshLocked(account, key string, intent api.TaskToolReleaseIntent, configurationOnly bool) bool {
	if !m.taskToolReleaseKeyFreshLocked(account, key) {
		return false
	}
	for _, pin := range intent.Apps {
		app, ok := m.apps[pin.Review.AppID]
		if !ok || app.AccountID != account || app.Slug != pin.App || app.Status != AppActive {
			return false
		}
		if app.OrgID != "" {
			org, ok := m.orgs[app.OrgID]
			if !ok || !org.Personal || org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != account {
				return false
			}
		}
		current, err := m.releaseReviewLocked(app.ID, api.DefaultEnvScope)
		if configurationOnly {
			current.LatestDeploymentID = pin.Review.LatestDeploymentID
			current.ServingDeploymentIDs = pin.Review.ServingDeploymentIDs
		}
		if err != nil || checkReleaseReview(pin.Review, current) != nil {
			return false
		}
	}
	return true
}

func (m *MemStore) PrepareTaskToolReleaseIntent(_ context.Context, account, key, id string, intent api.TaskToolReleaseIntent) (api.TaskToolReleaseIntentRecord, error) {
	record, err := tasktoolrelease.New(account, key, id, intent, time.Now())
	if err != nil {
		return record, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.taskToolReleaseIntents[account+":"+id]; ok {
		if old.RequesterKeyID != key || !reflect.DeepEqual(old.Intent, intent) {
			return record, ErrConflict
		}
		return tasktoolrelease.View(cloneTaskToolRelease(old), time.Now()), nil
	}
	if !m.taskToolReleaseFreshLocked(account, key, intent, false) {
		return record, ErrConflict
	}
	count := 0
	for _, old := range m.taskToolReleaseIntents {
		if old.AccountID == account {
			count++
		}
	}
	if count >= api.TaskToolReleaseIntentsPerAccount {
		return record, ErrTaskToolReleaseLimit
	}
	if m.taskToolReleaseIntents == nil {
		m.taskToolReleaseIntents = map[string]api.TaskToolReleaseIntentRecord{}
	}
	m.taskToolReleaseIntents[account+":"+id] = cloneTaskToolRelease(record)
	return record, nil
}

func (m *MemStore) GetTaskToolReleaseIntent(_ context.Context, account, id string) (api.TaskToolReleaseIntentRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.taskToolReleaseIntents[account+":"+id]
	if !ok {
		return record, ErrNotFound
	}
	return tasktoolrelease.View(cloneTaskToolRelease(record), time.Now()), nil
}

func (m *MemStore) ChangeTaskToolReleaseIntent(ctx context.Context, account, id, action string, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	release, err := m.lockTaskToolEffect(ctx, account, id)
	if err != nil {
		return api.TaskToolReleaseIntentRecord{}, err
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.taskToolReleaseIntents[account+":"+id]
	if !ok {
		return record, ErrNotFound
	}
	if tasktoolrelease.IsRetirementChange(action, in) {
		out, err := m.changeTaskRetirementLocked(account, record, in)
		if err == nil {
			m.taskToolReleaseIntents[account+":"+id] = cloneTaskToolRelease(out)
		}
		return cloneTaskToolRelease(out), err
	}
	if tasktoolrelease.IsQuarantineChange(action, in) {
		out, err := m.changeTaskQuarantineLocked(ctx, account, record, in)
		if err == nil {
			m.taskToolReleaseIntents[account+":"+id] = cloneTaskToolRelease(out)
		}
		return cloneTaskToolRelease(out), err
	}
	configurationOnly := tasktoolrelease.IsRecoveryDecision(in.Decision) || action == "execution" && in.Execution != nil && in.Execution.Action != "claim"
	freshKey := record.RequesterKeyID
	if tasktoolrelease.IsRecoveryDecision(in.Decision) {
		freshKey = in.AuthorizerKeyID
	}
	fresh := m.taskToolReleaseFreshLocked(account, freshKey, record.Intent, configurationOnly)
	if tasktoolrelease.IsRecoveryDecision(in.Decision) && in.Recovery != nil {
		snapshot := in.Recovery.Qualification
		if snapshot == nil && record.Execution != nil && record.Execution.Recovery != nil {
			snapshot = record.Execution.Recovery.Qualification
		}
		if snapshot != nil {
			fresh = fresh && m.taskToolReleaseFreshLocked(account, in.AuthorizerKeyID, api.TaskToolReleaseIntent{Apps: snapshot.NativeApps}, false)
		}
	}
	if action == "execution" || in.Decision == "authorize" || tasktoolrelease.IsRecoveryDecision(in.Decision) {
		owner := in.AuthorizerKeyID
		if action == "execution" && record.Execution != nil {
			owner = record.Execution.AuthorizerKeyID
		}
		k, ok := m.keys[owner]
		ownerFresh := ok && m.taskToolReleaseKeyFreshLocked(account, owner) && slices.Contains(k.Scopes, api.ScopeAdmin) && !slices.Contains(k.Scopes, api.ScopeAgent) && k.Label != api.MCPLoginKeyLabel
		if tasktoolrelease.IsRecoveryDecision(in.Decision) && !ownerFresh {
			return record, ErrConflict
		}
		fresh = fresh && ownerFresh
	}
	out, err := changeTaskToolRelease(record, action, in, fresh)
	if err == nil {
		m.taskToolReleaseIntents[account+":"+id] = cloneTaskToolRelease(out)
	}
	return cloneTaskToolRelease(out), err
}
