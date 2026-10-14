package state

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type ObjectComputeTriggerStore interface {
	ObjectNotificationStore
	ReconcileObjectBucketComputeTriggers(context.Context, string, string, string, api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error)
}

var _ ObjectComputeTriggerStore = (*MemStore)(nil)
var _ ObjectComputeTriggerStore = (*PgStore)(nil)

func objectNotificationChange(b ObjectBucket, current api.ObjectBucketNotifications, manual []api.ObjectNotificationRule, request *api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error) {
	if request == nil {
		current.Rules = api.CloneObjectNotificationRules(manual)
		current.ManagedRuleIDs = nil // Manual replacement explicitly takes control.
		return current, nil
	}
	if request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || *request.ExpectedRevision > api.MaxObjectStoragePolicyValue || request.Triggers == nil {
		return current, ErrObjectNotificationInvalid
	}
	if current.Revision != *request.ExpectedRevision {
		return current, ErrConflict
	}
	rules, err := api.ObjectStorageComputeTriggerRules(b.ID, b.Region, b.AccountID, b.AppID, request.Triggers)
	if err != nil {
		return current, ErrObjectNotificationInvalid
	}
	owned := make(map[string]bool, len(current.ManagedRuleIDs))
	for _, id := range current.ManagedRuleIDs {
		owned[id] = true
	}
	merged := make([]api.ObjectNotificationRule, 0, len(current.Rules)+len(rules))
	manualIDs := map[string]bool{}
	for _, rule := range current.Rules {
		if !owned[rule.ID] {
			merged = append(merged, rule)
			manualIDs[rule.ID] = true
		}
	}
	current.ManagedRuleIDs = nil
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, rule := range rules {
		if manualIDs[rule.ID] {
			return current, ErrConflict
		}
		merged = append(merged, rule)
		current.ManagedRuleIDs = append(current.ManagedRuleIDs, rule.ID)
	}
	current.Rules, err = api.NormalizeObjectNotificationRules(merged)
	if err != nil {
		return current, ErrObjectNotificationInvalid
	}
	return current, nil
}

func sameObjectNotificationPolicy(a, b api.ObjectBucketNotifications) bool {
	// nil and [] represent the same empty policy on the wire and in PostgreSQL.
	x, _ := json.Marshal(api.CloneObjectNotificationRules(a.Rules))
	y, _ := json.Marshal(api.CloneObjectNotificationRules(b.Rules))
	return string(x) == string(y) && reflect.DeepEqual(a.ManagedRuleIDs, b.ManagedRuleIDs)
}

func managedObjectNotificationRules(p api.ObjectBucketNotifications) []api.ObjectNotificationRule {
	ids := map[string]bool{}
	for _, id := range p.ManagedRuleIDs {
		ids[id] = true
	}
	out := []api.ObjectNotificationRule{}
	for _, rule := range p.Rules {
		if ids[rule.ID] {
			out = append(out, rule)
		}
	}
	return out
}

func decodeManagedObjectNotifications(p api.ObjectBucketNotifications, raw []byte) (api.ObjectBucketNotifications, error) {
	var managed []api.ObjectNotificationRule
	if err := json.Unmarshal(raw, &managed); err != nil {
		return p, ErrObjectNotificationInvalid
	}
	seen := map[string]bool{}
	for _, rule := range managed {
		if rule.ID == "" || seen[rule.ID] {
			return p, ErrObjectNotificationInvalid
		}
		seen[rule.ID] = true
		found := false
		for _, current := range p.Rules {
			if reflect.DeepEqual(rule, current) {
				found = true
				break
			}
		}
		if !found {
			return p, ErrObjectNotificationInvalid
		}
		p.ManagedRuleIDs = append(p.ManagedRuleIDs, rule.ID)
	}
	return p, nil
}
