package state

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// A full clone copies each captured bucket's notification configuration into
// its target bucket, paused as a whole (ADR-20261104120000 amendment
// 2026-11-16). The rules resolve in the target's environment, so a copied
// rule reaches the stage's own queue or deployment once resumed.

// ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable refuses
// a source bucket whose notification rule names a queue or function the stage
// will not have: an app outside the captured workloads, or a queue its
// workload does not hold in the source environment.
var ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable = fmt.Errorf("clone bucket notification destination is unavailable: %w", ErrConflict)

// ProjectEnvironmentCloneObjectNotifications is a captured bucket's
// notification configuration. ManagedRuleIDs mark the manifest compute
// triggers among the rules, which stay managed in the copy.
type ProjectEnvironmentCloneObjectNotifications struct {
	Revision       int64                        `json:"revision"`
	Rules          []api.ObjectNotificationRule `json:"rules"`
	ManagedRuleIDs []string                     `json:"managed_rule_ids"`
}

// cloneObjectNotificationsFrom captures a stored configuration.
func cloneObjectNotificationsFrom(p api.ObjectBucketNotifications) *ProjectEnvironmentCloneObjectNotifications {
	return &ProjectEnvironmentCloneObjectNotifications{Revision: p.Revision, Rules: api.CloneObjectNotificationRules(p.Rules),
		ManagedRuleIDs: slices.Sorted(slices.Values(p.ManagedRuleIDs))}
}

// clonedObjectNotifications is the configuration a target bucket stores for
// the capture.
func clonedObjectNotifications(bucketID string, n *ProjectEnvironmentCloneObjectNotifications) api.ObjectBucketNotifications {
	return api.ObjectBucketNotifications{BucketID: bucketID, Revision: n.Revision, Rules: api.CloneObjectNotificationRules(n.Rules),
		ManagedRuleIDs: slices.Sorted(slices.Values(n.ManagedRuleIDs))}
}

// requireCloneNotificationDestinations refuses a capture whose buckets name a
// destination the stage will not hold.
func requireCloneNotificationDestinations(records []projectCloneWorkloadRecord) error {
	apps := map[string]bool{}
	queues := map[string]bool{}
	for _, record := range records {
		apps[canonicalMemUUID(record.AppID)] = true
		if record.snapshot.Policies != nil && record.snapshot.Policies.Queues != nil {
			for _, binding := range record.snapshot.Policies.Queues.Bindings {
				queues[canonicalMemUUID(record.AppID)+"\x00"+binding.QueueName] = true
			}
		}
	}
	for _, record := range records {
		if record.snapshot.Bindings == nil {
			continue
		}
		for _, bucket := range record.snapshot.Bindings.Buckets {
			if bucket.Notifications == nil {
				continue
			}
			for _, r := range bucket.Notifications.Rules {
				t, err := api.ParseObjectNotificationTarget(r.Destination)
				if err != nil || !apps[canonicalMemUUID(t.AppID)] {
					return ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable
				}
				if t.Kind == "queue" && !queues[canonicalMemUUID(t.AppID)+"\x00"+t.QueueName] {
					return ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable
				}
			}
		}
	}
	return nil
}

// validateCloneNotificationCopy proves a target bucket holds exactly the
// captured configuration, still paused; a bucket captured without one holds
// none.
func validateCloneNotificationCopy(source *ProjectEnvironmentCloneObjectNotifications, target api.ObjectBucketNotifications, present, paused bool) error {
	if source == nil {
		if present && (len(target.Rules) > 0 || len(target.ManagedRuleIDs) > 0) {
			return fmt.Errorf("clone target bucket has notifications its source had none of: %w", ErrConflict)
		}
		return nil
	}
	want := clonedObjectNotifications(target.BucketID, source)
	got := target
	got.ManagedRuleIDs = slices.Sorted(slices.Values(got.ManagedRuleIDs))
	if !present || !paused || !sameObjectNotificationPolicy(want, got) {
		return fmt.Errorf("clone target bucket notifications differ from their capture: %w", ErrConflict)
	}
	return nil
}

// CloneBucketNotificationResumeStore resumes the bucket notifications a full
// clone paused in one environment and returns the buckets it resumed.
type CloneBucketNotificationResumeStore interface {
	ResumeEnvironmentClonePausedBucketNotifications(ctx context.Context, accountID, projectID, environment string) ([]string, error)
}

func (m *MemStore) copyCloneBucketNotificationsLocked(source ProjectEnvironmentCloneObjectBucket, targetID string) {
	if source.Notifications == nil {
		return
	}
	if _, exists := m.objectNotifications[targetID]; exists {
		return
	}
	if m.objectNotifications == nil {
		m.objectNotifications = map[string]api.ObjectBucketNotifications{}
	}
	if m.objectNotificationClonePaused == nil {
		m.objectNotificationClonePaused = map[string]bool{}
	}
	m.objectNotifications[targetID] = clonedObjectNotifications(targetID, source.Notifications)
	m.objectNotificationClonePaused[targetID] = true
}

func (m *MemStore) verifyCloneBucketNotificationPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	for _, record := range records {
		if record.snapshot.Bindings == nil {
			continue
		}
		for _, source := range record.snapshot.Bindings.Buckets {
			for _, target := range m.objectBuckets {
				if target.EnvironmentCloneOperationID != op.ID || target.EnvironmentCloneSourceBucketID != source.ID || target.State == "deleted" {
					continue
				}
				stored, present := m.objectNotifications[target.ID]
				stored.BucketID = target.ID
				if err := validateCloneNotificationCopy(source.Notifications, stored, present, m.objectNotificationClonePaused[target.ID]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *MemStore) ResumeEnvironmentClonePausedBucketNotifications(_ context.Context, accountID, projectID, environment string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if IsProductionScope(environment) {
		return nil, ErrInvalidArgument
	}
	found := false
	for _, env := range m.projectEnvironments {
		if env.AccountID == accountID && env.ProjectID == projectID && env.Slug == environment {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	var resumed []string
	for id := range m.objectNotificationClonePaused {
		b, ok := m.objectBuckets[id]
		if !ok || b.AccountID != accountID || b.Scope != environment || b.EnvironmentCloneOperationID == "" {
			continue
		}
		if op, ok := m.projectEnvironmentCloneOperations[b.EnvironmentCloneOperationID]; !ok || op.ProjectID != projectID {
			continue
		}
		delete(m.objectNotificationClonePaused, id)
		resumed = append(resumed, id)
	}
	sort.Strings(resumed)
	return resumed, nil
}
