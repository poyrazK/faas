package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// This private configuration catalogue contains policy definitions and
// producer bindings. Runtime lanes, queued work and cancellation receipts are
// operational state and are not copied into a new environment.
type ProjectEnvironmentCloneWorkPolicyDefinitions struct {
	Version         int                                               `json:"version"`
	AppID           string                                            `json:"app_id"`
	SourceScope     string                                            `json:"source_scope"`
	Policies        []ProjectEnvironmentCloneWorkPolicy               `json:"policies"`
	EventBindings   []ProjectEnvironmentCloneEventWorkPolicyBinding   `json:"event_bindings"`
	TriggerBindings []ProjectEnvironmentCloneTriggerWorkPolicyBinding `json:"trigger_bindings"`
}

type ProjectEnvironmentCloneWorkPolicy struct {
	Name                     string `json:"name"`
	Revision                 int64  `json:"revision"`
	MaxRunningPerKey         int    `json:"max_running_per_key"`
	MaxRunningPerFairnessKey int    `json:"max_running_per_fairness_key"`
	PendingUpdates           string `json:"pending_updates"`
	DebounceMS               int64  `json:"debounce_ms"`
	ExpiresAfterMS           int64  `json:"expires_after_ms"`
}

type ProjectEnvironmentCloneEventWorkPolicyBinding struct {
	SubscriptionID   string `json:"subscription_id"`
	PolicyName       string `json:"policy_name"`
	KeySelector      string `json:"key_selector"`
	FairnessSelector string `json:"fairness_selector"`
	Action           string `json:"action"`
}

type ProjectEnvironmentCloneTriggerWorkPolicyBinding struct {
	TriggerID        string `json:"trigger_id"`
	PolicyName       string `json:"policy_name"`
	KeySelector      string `json:"key_selector"`
	FairnessSelector string `json:"fairness_selector"`
}

type ProjectEnvironmentCloneWorkPolicyCapture struct {
	OperationID, Hash string
	Definitions       ProjectEnvironmentCloneWorkPolicyDefinitions
}

type ProjectEnvironmentCloneWorkPolicyCaptureStore interface {
	ProjectEnvironmentCloneWorkPoliciesForLease(context.Context, ProjectEnvironmentCloneLease) ([]ProjectEnvironmentCloneWorkPolicyCapture, error)
}

var ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable = fmt.Errorf("clone work policy isolation proof is unavailable: %w", ErrConflict)

func normalizeCloneWorkPolicyDefinitions(definitions ProjectEnvironmentCloneWorkPolicyDefinitions) (ProjectEnvironmentCloneWorkPolicyDefinitions, error) {
	if definitions.Version != 1 || !validCloneCredentialSourceID(definitions.AppID) || api.ValidateScope(definitions.SourceScope) != nil {
		return definitions, ErrConflict
	}
	definitions.Policies = append([]ProjectEnvironmentCloneWorkPolicy{}, definitions.Policies...)
	definitions.EventBindings = append([]ProjectEnvironmentCloneEventWorkPolicyBinding{}, definitions.EventBindings...)
	definitions.TriggerBindings = append([]ProjectEnvironmentCloneTriggerWorkPolicyBinding{}, definitions.TriggerBindings...)
	policies := map[string]bool{}
	for _, policy := range definitions.Policies {
		if policy.Revision < 1 || policies[policy.Name] || policy.PendingUpdates == "" || policy.DebounceMS < 0 || policy.ExpiresAfterMS < 0 ||
			policy.DebounceMS > int64(workpolicy.MaxDebounce/time.Millisecond) || policy.ExpiresAfterMS > int64(workpolicy.MaxExpiresAfter/time.Millisecond) {
			return definitions, ErrConflict
		}
		value := workpolicy.Policy{Name: policy.Name, MaxRunningPerKey: policy.MaxRunningPerKey, MaxRunningPerFairnessKey: policy.MaxRunningPerFairnessKey,
			PendingUpdates: workpolicy.PendingUpdates(policy.PendingUpdates), Debounce: time.Duration(policy.DebounceMS) * time.Millisecond, ExpiresAfter: time.Duration(policy.ExpiresAfterMS) * time.Millisecond}
		if value.Validate() != nil {
			return definitions, ErrConflict
		}
		policies[policy.Name] = true
	}
	events, triggers := map[string]bool{}, map[string]bool{}
	for _, binding := range definitions.EventBindings {
		if !validCloneCredentialSourceID(binding.SubscriptionID) || events[binding.SubscriptionID] || !policies[binding.PolicyName] ||
			(binding.Action != EventWorkInvoke && binding.Action != EventWorkCancelPending) || !validCloneWorkSelectors(binding.KeySelector, binding.FairnessSelector) {
			return definitions, ErrConflict
		}
		events[binding.SubscriptionID] = true
	}
	for _, binding := range definitions.TriggerBindings {
		if !validCloneCredentialSourceID(binding.TriggerID) || triggers[binding.TriggerID] || !policies[binding.PolicyName] || !validCloneWorkSelectors(binding.KeySelector, binding.FairnessSelector) {
			return definitions, ErrConflict
		}
		triggers[binding.TriggerID] = true
	}
	sort.Slice(definitions.Policies, func(i, j int) bool { return definitions.Policies[i].Name < definitions.Policies[j].Name })
	sort.Slice(definitions.EventBindings, func(i, j int) bool {
		return definitions.EventBindings[i].SubscriptionID < definitions.EventBindings[j].SubscriptionID
	})
	sort.Slice(definitions.TriggerBindings, func(i, j int) bool {
		return definitions.TriggerBindings[i].TriggerID < definitions.TriggerBindings[j].TriggerID
	})
	return definitions, nil
}

func validCloneWorkSelectors(key, fairness string) bool {
	if _, err := workpolicy.ParseSelector(key); err != nil {
		return false
	}
	if fairness != "" {
		if _, err := workpolicy.ParseSelector(fairness); err != nil {
			return false
		}
	}
	return true
}

func cloneWorkPolicyDefinitionsHash(definitions ProjectEnvironmentCloneWorkPolicyDefinitions) (string, error) {
	definitions, err := normalizeCloneWorkPolicyDefinitions(definitions)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}
