package state

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectNotificationStore = (*MemStore)(nil)

func (m *MemStore) ownedNotificationsLocked(account, app, bucket string) (ObjectBucket, api.ObjectBucketNotifications, error) {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return b, api.ObjectBucketNotifications{}, ErrNotFound
	}
	if b.State != "ready" {
		return b, api.ObjectBucketNotifications{}, ErrConflict
	}
	p := m.objectNotifications[bucket]
	p.BucketID = bucket
	p.Rules = api.CloneObjectNotificationRules(p.Rules)
	return b, p, nil
}
func (m *MemStore) GetObjectBucketNotifications(_ context.Context, account, app, bucket string) (api.ObjectBucketNotifications, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, p, err := m.ownedNotificationsLocked(account, app, bucket)
	return p, err
}
func (m *MemStore) notificationQueueLocked(t api.ObjectNotificationTarget) (string, []byte, error) {
	for _, b := range m.queueBindings {
		if notificationSameIdentity(b.AccountID, t.AccountID) && notificationSameIdentity(b.AppID, t.AppID) && b.QueueName == t.QueueName && b.Enabled {
			return b.ID, bytes.Clone(b.RetryPolicyJSON), nil
		}
	}
	return "", nil, ErrNotFound
}
func (m *MemStore) SetObjectBucketNotifications(_ context.Context, account, app, bucket string, rules []api.ObjectNotificationRule) (api.ObjectBucketNotifications, error) {
	normal, err := api.NormalizeObjectNotificationRules(rules)
	if err != nil {
		return api.ObjectBucketNotifications{}, ErrObjectNotificationInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, p, err := m.ownedNotificationsLocked(account, app, bucket)
	if err != nil {
		return p, err
	}
	for _, r := range normal {
		t, e := notificationTarget(r, b)
		if e != nil {
			return p, e
		}
		a, ok := m.eventSubscriptionAppLocked(t.AppID)
		if !ok || !notificationSameIdentity(a.AccountID, account) || a.Status == AppDeleted || !notificationEntitled(m.accounts[account].Plan, t.Kind) {
			return p, ErrObjectNotificationInvalid
		}
		if t.Kind == "queue" {
			if _, _, e = m.notificationQueueLocked(t); e != nil {
				return p, ErrObjectNotificationInvalid
			}
		}
	}
	a, _ := json.Marshal(p.Rules)
	z, _ := json.Marshal(normal)
	if bytes.Equal(a, z) {
		return p, nil
	}
	if p.Revision >= api.MaxObjectStoragePolicyValue {
		return p, ErrConflict
	}
	p.Revision++
	p.Rules = normal
	if m.objectNotifications == nil {
		m.objectNotifications = map[string]api.ObjectBucketNotifications{}
	}
	m.objectNotifications[bucket] = p
	p.Rules = api.CloneObjectNotificationRules(p.Rules)
	return p, nil
}
func (m *MemStore) EnqueueObjectNotification(_ context.Context, in Invocation, s ObjectNotificationSnapshot) error {
	t, err := validateNotificationInvocation(in, s)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.invocations[in.ID]; ok {
		if old.AppID == in.AppID && old.AccountID == in.AccountID && old.Source == in.Source && old.QueueName == in.QueueName && jsonEqual(old.Payload, in.Payload) {
			return nil
		}
		return ErrConflict
	}
	a, ok := m.eventSubscriptionAppLocked(in.AppID)
	if !ok || !notificationSameIdentity(a.AccountID, in.AccountID) || a.Status == AppDeleted {
		return ErrNotFound
	}
	if !notificationEntitled(m.accounts[in.AccountID].Plan, t.Kind) {
		return ErrObjectNotificationCapacity
	}
	if t.Kind == "queue" {
		binding, _, e := m.notificationQueueLocked(t)
		if e != nil || binding != s.QueueBindingID {
			return ErrNotFound
		}
		n := 0
		for _, row := range m.invocations {
			if row.AppID == in.AppID && row.Source == InvocationQueue && (row.State == InvocationPending || row.State == InvocationDispatching) {
				n++
			}
		}
		if n >= api.MustLimitsFor(m.accounts[in.AccountID].Plan).MaxQueueDepth {
			return ErrObjectNotificationCapacity
		}
	}
	if err = m.platformTenantInvocationAllowedLocked(in); err != nil {
		return err
	}
	in.State = InvocationPending
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	in.Payload = bytes.Clone(in.Payload)
	in.Headers = bytes.Clone(in.Headers)
	in.RetryPolicyJSON = bytes.Clone(s.RetryPolicy)
	m.setInvocationLocked(in.ID, in)
	return nil
}
