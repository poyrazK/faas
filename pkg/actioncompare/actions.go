// Package actioncompare compares sealed, platform-captured business intent.
// Its versioned scope distinguishes managed intent and simulated relay attempts.
package actioncompare

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

const Version = 1
const Provenance = "gregale_captured_managed_webhooks"
const SimulationProvenance = "gregale_captured_rehearsal_actions"

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var runPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var workloadPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var typePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,255}$`)

type Evidence struct {
	Version        int                         `json:"version"`
	Status         string                      `json:"status"`
	Provenance     string                      `json:"provenance"`
	ContractSHA256 string                      `json:"contract_sha256,omitempty"`
	FixtureSHA256  string                      `json:"fixture_sha256,omitempty"`
	Snapshot       *api.ScenarioActionSnapshot `json:"snapshot,omitempty"`
	Error          string                      `json:"error,omitempty"`
}

type Value struct {
	ReceiverSHA256 string          `json:"receiver_sha256"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
}

type Change struct {
	Key      string  `json:"key"`
	Workload string  `json:"workload"`
	Source   string  `json:"source"`
	Workflow string  `json:"workflow,omitempty"`
	Step     string  `json:"step,omitempty"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Before   []Value `json:"before"`
	After    []Value `json:"after"`
}

type Comparison struct {
	Status      string   `json:"status"`
	Reason      string   `json:"reason,omitempty"`
	Provenance  string   `json:"provenance"`
	Scope       string   `json:"scope"`
	BeforeCount *int     `json:"before_count,omitempty"`
	AfterCount  *int     `json:"after_count,omitempty"`
	Changes     []Change `json:"changes,omitempty"`
}

// Compare treats each logical effect as a multiset of business intents. It
// excludes generated runtime IDs and commit order, and preserves multiplicity,
// exact payload values, effect names and snapshotted destination configuration.
func Compare(before, after *Evidence) *Comparison {
	if before == nil && after == nil {
		return nil
	}
	result := &Comparison{Status: "inconclusive", Provenance: Provenance, Scope: api.ScenarioActionScope}
	for _, e := range []*Evidence{before, after} {
		if e != nil && e.Snapshot != nil && e.Snapshot.Version == api.ScenarioSimulationSnapshotVersion {
			result.Scope, result.Provenance = api.ScenarioSimulationScope, SimulationProvenance
			break
		}
	}
	if before == nil || after == nil {
		result.Reason = "captured action evidence is missing from one report"
		return result
	}
	old, oldErr := validateEvidence(before)
	current, newErr := validateEvidence(after)
	if oldErr != nil || newErr != nil {
		result.Reason = "captured action evidence is incomplete, unsealed or invalid"
		return result
	}
	if before.ContractSHA256 != after.ContractSHA256 || before.FixtureSHA256 != after.FixtureSHA256 {
		result.Reason = "captured action recipe, observers or fixture identity changed"
		return result
	}
	if before.Snapshot.Version != after.Snapshot.Version || before.Snapshot.Scope != after.Snapshot.Scope || before.Snapshot.SimulationSHA256 != after.Snapshot.SimulationSHA256 ||
		before.Snapshot.EventCaptureVersion != after.Snapshot.EventCaptureVersion || before.Snapshot.QueueCaptureVersion != after.Snapshot.QueueCaptureVersion || managedWorkDigest(before.Snapshot) != managedWorkDigest(after.Snapshot) || coldStartDigest(before.Snapshot) != coldStartDigest(after.Snapshot) || suspendResumeDigest(before.Snapshot) != suspendResumeDigest(after.Snapshot) {
		result.Reason = "captured action scope or simulation fixtures changed"
		return result
	}
	result.Scope = before.Snapshot.Scope
	result.Provenance = before.Provenance
	oldWorkloads, newWorkloads := append([]string{}, before.Snapshot.Workloads...), append([]string{}, after.Snapshot.Workloads...)
	sort.Strings(oldWorkloads)
	sort.Strings(newWorkloads)
	a, _ := json.Marshal(oldWorkloads)
	b, _ := json.Marshal(newWorkloads)
	if !bytes.Equal(a, b) {
		result.Reason = "captured action workload inventory changed"
		return result
	}
	oldCount, newCount := len(before.Snapshot.Intents)+len(before.Snapshot.SimulatedAttempts)+capturedEventCount(before.Snapshot)+len(before.Snapshot.QueueMessages), len(after.Snapshot.Intents)+len(after.Snapshot.SimulatedAttempts)+capturedEventCount(after.Snapshot)+len(after.Snapshot.QueueMessages)
	if before.Snapshot.ManagedWork != nil {
		oldCount += len(before.Snapshot.ManagedWork.Jobs)
		newCount += len(after.Snapshot.ManagedWork.Jobs)
	}
	result.Status, result.BeforeCount, result.AfterCount = "passed", &oldCount, &newCount
	if postgresFaultBudgetExceeded(before.Snapshot) || postgresFaultBudgetExceeded(after.Snapshot) {
		result.Status, result.Reason = "failed", "PostgreSQL recovery budget exceeded"
	}
	if suspendResumeBudgetExceeded(before.Snapshot) || suspendResumeBudgetExceeded(after.Snapshot) {
		result.Status, result.Reason = "failed", "suspend/resume readiness budget exceeded"
	}
	if coldBudgetExceeded(before.Snapshot) || coldBudgetExceeded(after.Snapshot) {
		result.Status = "failed"
	}
	keys := make([]string, 0, len(old)+len(current))
	seen := map[string]bool{}
	for key := range old {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range current {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		left, hasBefore := old[key]
		right, hasAfter := current[key]
		change := left
		if !hasBefore {
			change = right
		}
		change.Before, change.After = left.Before, right.Before
		if change.Before == nil {
			change.Before = []Value{}
		}
		if change.After == nil {
			change.After = []Value{}
		}
		change.Status = "unchanged"
		beforeJSON, _ := json.Marshal(change.Before)
		afterJSON, _ := json.Marshal(change.After)
		if !bytes.Equal(beforeJSON, afterJSON) {
			result.Status = "failed"
			change.Status = "changed"
			if !hasBefore {
				change.Status = "added"
			} else if !hasAfter {
				change.Status = "removed"
			}
		}
		result.Changes = append(result.Changes, change)
	}
	return result
}

func validateEvidence(evidence *Evidence) (map[string]Change, error) {
	if evidence.Version != Version || evidence.Provenance != expectedProvenance(evidence.Snapshot) || evidence.Status != "complete" || evidence.Error != "" ||
		!digestPattern.MatchString(evidence.ContractSHA256) || !digestPattern.MatchString(evidence.FixtureSHA256) {
		return nil, errors.New("invalid capture provenance or input identity")
	}
	return ValidateSnapshot(evidence.Snapshot)
}

// ValidateSnapshot never accepts a partial inventory as an empty successful run.
func ValidateSnapshot(snapshot *api.ScenarioActionSnapshot) (map[string]Change, error) {
	if snapshot == nil || !validSnapshotScope(snapshot) || snapshot.Status != "complete" ||
		!runPattern.MatchString(snapshot.RunID) || snapshot.Truncated || snapshot.Reason != "" || snapshot.PendingManagedWork != 0 || snapshot.IncompleteManagedEffects != 0 || snapshot.SuppressedDeliveries < 0 ||
		snapshot.CapturedCount != int64(len(snapshot.Intents)) || len(snapshot.Intents) > api.MaxScenarioActionIntents || snapshot.Intents == nil || len(snapshot.Workloads) == 0 || len(snapshot.Workloads) > 16 {
		return nil, errors.New("invalid snapshot completeness")
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.SealedAt); err != nil {
		return nil, errors.New("missing seal time")
	}
	body, err := json.Marshal(snapshot)
	if err != nil || len(body) > api.MaxScenarioActionSnapshotBytes {
		return nil, errors.New("snapshot exceeds the format bounds")
	}
	workloads := map[string]bool{}
	for _, name := range snapshot.Workloads {
		if !workloadPattern.MatchString(name) || workloads[name] {
			return nil, errors.New("invalid snapshot workloads")
		}
		workloads[name] = true
	}
	groups := map[string]Change{}
	ids := map[string]bool{}
	total := 0
	for _, intent := range snapshot.Intents {
		id, err := uuid.Parse(intent.ID)
		if err != nil || ids[id.String()] || !workloads[intent.Workload] || !namePattern.MatchString(intent.Name) || !typePattern.MatchString(intent.Type) || !digestPattern.MatchString(intent.ReceiverSHA256) {
			return nil, errors.New("invalid captured intent identity")
		}
		ids[id.String()] = true
		if _, err := uuid.Parse(intent.OperationID); err != nil {
			return nil, errors.New("invalid captured operation identity")
		}
		if _, err := uuid.Parse(intent.WebhookID); err != nil {
			return nil, errors.New("invalid captured receiver identity")
		}
		if intent.Source != "operation" && intent.Source != "workflow" || intent.Source == "operation" && (intent.Workflow != "" || intent.Step != "") || intent.Source == "workflow" && (intent.Workflow == "" || intent.Step == "") {
			return nil, errors.New("invalid captured intent source")
		}
		total += len(intent.Payload)
		if len(intent.Payload) > api.MaxExclusiveEffectPayloadBytes || total > api.MaxScenarioActionPayloadBytes {
			return nil, errors.New("captured payload exceeds the format bounds")
		}
		payload, err := behaviorcompare.CanonicalJSON(intent.Payload)
		if err != nil {
			return nil, errors.New("captured payload is ambiguous or invalid")
		}
		keyBody, _ := json.Marshal([]string{intent.Workload, intent.Source, intent.Workflow, intent.Step, intent.Name})
		digest := sha256.Sum256(keyBody)
		key := hex.EncodeToString(digest[:])
		group := groups[key]
		if group.Key == "" {
			group = Change{Key: key, Workload: intent.Workload, Source: intent.Source, Workflow: intent.Workflow, Step: intent.Step, Name: intent.Name}
		}
		group.Before = append(group.Before, Value{ReceiverSHA256: intent.ReceiverSHA256, Type: intent.Type, Payload: payload})
		groups[key] = group
	}
	if err := addSimulationGroups(snapshot, groups, ids, total); err != nil {
		return nil, err
	}
	if err := addSuspendResumeGroups(snapshot, groups); err != nil {
		return nil, err
	}
	if err := addColdStartGroups(snapshot, groups); err != nil {
		return nil, err
	}
	if err := addManagedWorkGroups(snapshot, groups, ids); err != nil {
		return nil, err
	}
	if err := addEventGroups(snapshot, groups, ids); err != nil {
		return nil, err
	}
	if err := addQueueGroups(snapshot, groups, ids); err != nil {
		return nil, err
	}
	for key, group := range groups {
		sort.Slice(group.Before, func(i, j int) bool {
			a, _ := json.Marshal(group.Before[i])
			b, _ := json.Marshal(group.Before[j])
			return bytes.Compare(a, b) < 0
		})
		groups[key] = group
	}
	return groups, nil
}
