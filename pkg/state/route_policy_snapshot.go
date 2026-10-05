package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DeploymentRoutePolicySnapshotSchemaVersion versions the persisted rule
// envelope independently from the OpenAPI contract snapshot.
const DeploymentRoutePolicySnapshotSchemaVersion = 1

type deploymentRoutePolicyEnvelope struct {
	SchemaVersion int                    `json:"schema_version"`
	Rules         []api.EdgeRuleResponse `json:"rules"`
}

// marshalDeploymentRoutePolicySnapshot converts the store's domain rows into
// the same rule shape returned by the app edge-rules API, then fingerprints a
// deterministic ordering. All rule actions and match values remain in the
// owner-scoped snapshot; report renderers redact them before sharing evidence.
func marshalDeploymentRoutePolicySnapshot(deploymentID, appID, scope string, rules []EdgeRule) (DeploymentRoutePolicySnapshot, error) {
	wireRules := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		action, err := json.Marshal(rule.Action)
		if err != nil {
			return DeploymentRoutePolicySnapshot{}, fmt.Errorf("marshal rule %s action: %w", rule.ID, err)
		}
		wireRules = append(wireRules, api.EdgeRuleResponse{
			ID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
			MatchMethods: append([]string(nil), rule.MatchMethods...),
			MatchHeaders: cloneStringMap(rule.MatchHeaders),
			Priority:     rule.Priority, Enabled: rule.Enabled, Kind: string(rule.Kind),
			ValidateMode: rule.ValidateMode, Action: action,
			CreatedAt: rule.CreatedAt, UpdatedAt: rule.UpdatedAt,
		})
	}
	sort.SliceStable(wireRules, func(i, j int) bool {
		if wireRules[i].Priority != wireRules[j].Priority {
			return wireRules[i].Priority < wireRules[j].Priority
		}
		if !wireRules[i].CreatedAt.Equal(wireRules[j].CreatedAt) {
			return wireRules[i].CreatedAt.After(wireRules[j].CreatedAt)
		}
		return wireRules[i].ID < wireRules[j].ID
	})
	raw, err := json.Marshal(deploymentRoutePolicyEnvelope{
		SchemaVersion: DeploymentRoutePolicySnapshotSchemaVersion,
		Rules:         wireRules,
	})
	if err != nil {
		return DeploymentRoutePolicySnapshot{}, fmt.Errorf("marshal rule envelope: %w", err)
	}
	sum := sha256.Sum256(raw)
	return DeploymentRoutePolicySnapshot{
		DeploymentID: deploymentID, AppID: appID, Scope: normalizedDeploymentScope(scope),
		Snapshot: raw, SHA256: hex.EncodeToString(sum[:]),
		SchemaVersion: DeploymentRoutePolicySnapshotSchemaVersion, CapturedAt: time.Now().UTC(),
	}, nil
}

// UnmarshalDeploymentRoutePolicySnapshot validates and decodes a persisted
// snapshot. Postgres jsonb normalizes object formatting, so the stored digest
// is retained as capture-time provenance instead of being recomputed from the
// database's re-serialized JSON text.
func UnmarshalDeploymentRoutePolicySnapshot(snap DeploymentRoutePolicySnapshot) ([]api.EdgeRuleResponse, error) {
	if err := validateDeploymentRoutePolicySnapshot(snap); err != nil {
		return nil, err
	}
	var envelope deploymentRoutePolicyEnvelope
	if err := json.Unmarshal(snap.Snapshot, &envelope); err != nil {
		return nil, fmt.Errorf("state: decode route policy snapshot: %w", err)
	}
	if envelope.SchemaVersion != snap.SchemaVersion || envelope.SchemaVersion != DeploymentRoutePolicySnapshotSchemaVersion {
		return nil, fmt.Errorf("state: unsupported route policy snapshot schema version %d", envelope.SchemaVersion)
	}
	if envelope.Rules == nil {
		envelope.Rules = []api.EdgeRuleResponse{}
	}
	return envelope.Rules, nil
}

func validateDeploymentRoutePolicySnapshot(snap DeploymentRoutePolicySnapshot) error {
	if snap.DeploymentID == "" || snap.AppID == "" || snap.Scope == "" {
		return errors.New("deployment_id, app_id, and scope are required")
	}
	if len(snap.Snapshot) == 0 || snap.SHA256 == "" {
		return errors.New("snapshot and sha256 are required")
	}
	if snap.SchemaVersion < 1 {
		return errors.New("schema_version must be >= 1")
	}
	if !regexpDeploymentRoutePolicySHA256.MatchString(snap.SHA256) {
		return errors.New("sha256 must be 64 lower-case hexadecimal characters")
	}
	if !regexpDeploymentScope.MatchString(normalizedDeploymentScope(snap.Scope)) {
		return errors.New("scope has invalid shape")
	}
	return nil
}

var regexpDeploymentScope = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)
var regexpDeploymentRoutePolicySHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
