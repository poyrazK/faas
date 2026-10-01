package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// Operational ownership is separate from the immutable policy configuration.
// These records and the queued work they authenticate are never cloned.
type InvocationWorkEnvironmentAdmission struct {
	InvocationID, EnvironmentID, WorkloadSpecID, SettingsHash, AppID, PolicyName string
	PolicyRevision                                                               int64
	KeyDigest, FairnessDigest                                                    []byte
	FairnessLimit                                                                int
}

type InvocationWorkEnvironmentAdmissionReader interface {
	InvocationWorkEnvironmentAdmission(context.Context, string) (InvocationWorkEnvironmentAdmission, error)
}

type invocationWorkEnvironment struct {
	app         App
	environment ProjectEnvironment
	spec        ProjectEnvironmentWorkloadSpec
	deployment  string
	policy      AppWorkPolicy
}

func invocationHasSharedWorkProducer(inv Invocation) bool {
	if inv.QueueName != "" || (inv.CronID != nil && *inv.CronID != "") {
		return true
	}
	switch inv.Source {
	case InvocationQueue, InvocationCron, InvocationInboundWebhook, InvocationReplay, InvocationSource("esm"):
		return true
	}
	return false
}

func stageKeyedInvocationSupported(inv Invocation) bool {
	return (inv.Source == InvocationAsyncInvoke || inv.Source == InvocationDelayedTask) && !invocationHasSharedWorkProducer(inv) &&
		inv.OnSuccessDestinationID == "" && inv.OnFailureDestinationID == ""
}

func invocationWorkDomainDigest(environmentID, kind, canonicalKey string) ([32]byte, error) {
	legacy, err := workpolicy.DigestKey(canonicalKey)
	if err != nil || environmentID == "" {
		return legacy, err
	}
	id, err := uuid.Parse(environmentID)
	if err != nil || id == uuid.Nil || (kind != "key" && kind != "fairness") {
		return [32]byte{}, ErrInvalidArgument
	}
	// The prefix cannot be a canonical scalar accepted by the legacy digest.
	// UUID bytes distinguish deletion/recreation of the same environment slug.
	hash := sha256.New()
	_, _ = hash.Write([]byte("gregale.work.environment.v1\x00" + kind + "\x00"))
	_, _ = hash.Write(id[:])
	_, _ = hash.Write(legacy[:])
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func workEnvironmentFairnessDigest(policy workpolicy.Policy, environmentID, key string, fairnessKeys []string) ([]byte, error) {
	if len(fairnessKeys) > 1 {
		return nil, ErrInvalidArgument
	}
	if policy.MaxRunningPerFairnessKey == 0 {
		return nil, nil
	}
	if len(fairnessKeys) == 1 {
		key = fairnessKeys[0]
	}
	digest, err := invocationWorkDomainDigest(environmentID, "fairness", key)
	return digest[:], err
}

func resolveKeyedInvocationEnvironment(ctx context.Context, store interface {
	invocationAppReader
	invocationPinScopeStore
}, inv Invocation, policy workpolicy.Policy) (Invocation, invocationWorkEnvironment, error) {
	if policy.Validate() != nil {
		return inv, invocationWorkEnvironment{}, ErrInvalidArgument
	}
	var headers map[string]string
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return inv, invocationWorkEnvironment{}, ErrInvalidArgument
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil || (revision == "" && release == "") {
		return inv, invocationWorkEnvironment{}, err
	}
	scope, err := store.ResolveInvocationPinScope(ctx, inv.AppID, revision, release)
	if err != nil || !invocationStageScope(scope) {
		return inv, invocationWorkEnvironment{}, err
	}
	if !stageKeyedInvocationSupported(inv) {
		return inv, invocationWorkEnvironment{}, ErrInvocationEnvironmentWorkIsolation
	}
	// Admission authenticates policy inputs before a row/proof exists. The
	// delivery resolver requires the persisted proof once those fields exist.
	base := inv
	base.ID, base.WorkPolicyName, base.WorkPolicyRevision = "", "", 0
	base.WorkKeyDigest, base.WorkFairnessDigest, base.WorkFairnessLimit = nil, nil, 0
	base.WorkSequence, base.WorkExpiresAt = 0, nil
	prepared, version, err := ResolveInvocationVersion(ctx, store, base)
	if err != nil {
		return inv, invocationWorkEnvironment{}, err
	}
	info, err := invocationWorkEnvironmentForVersion(ctx, store, inv.AppID, version, policy.Name)
	if err != nil {
		return inv, info, err
	}
	if policy.PendingUpdates == "" {
		policy.PendingUpdates = workpolicy.PendingAll
	}
	if policy != info.policy.Policy || (inv.WorkPolicyRevision != 0 && inv.WorkPolicyRevision != info.policy.Revision) ||
		(inv.WorkPolicyName != "" && inv.WorkPolicyName != policy.Name) {
		return inv, info, fmt.Errorf("pinned environment work policy differs from admission inputs: %w", ErrConflict)
	}
	inv.Headers, inv.WorkPolicyRevision = prepared.Headers, info.policy.Revision
	return inv, info, nil
}

func invocationWorkEnvironmentForVersion(ctx context.Context, store invocationAppReader, appID string, version InvocationVersion, policyName string) (invocationWorkEnvironment, error) {
	info := invocationWorkEnvironment{deployment: version.DeploymentID}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return info, err
	}
	reader, ok := store.(interface {
		DeploymentWorkloadSpecReader
		invocationEnvironmentStore
	})
	if !ok || !invocationStageScope(version.Scope) || version.DeploymentID == "" {
		return info, ErrInvocationEnvironmentWorkIsolation
	}
	info.app = app
	info.environment, err = reader.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, version.Scope)
	if err != nil {
		return info, err
	}
	info.spec, err = reader.ProjectEnvironmentWorkloadSpecForDeployment(ctx, app.AccountID, app.ProjectID, version.DeploymentID)
	if err != nil || validateEnvironmentWorkPolicySpec(info.spec, app, version.Scope) != nil || info.spec.Settings.WorkPolicies == nil ||
		info.spec.EnvironmentID != info.environment.ID || info.environment.AccountID != app.AccountID || info.environment.ProjectID != app.ProjectID {
		return info, ErrInvocationEnvironmentWorkIsolation
	}
	for _, policy := range info.spec.Settings.WorkPolicies.Policies {
		if policy.Name == policyName {
			info.policy = environmentWorkPolicyRecord(app, policy, info.spec.CreatedAt)
			return info, nil
		}
	}
	return info, ErrInvocationEnvironmentWorkIsolation
}

func (info invocationWorkEnvironment) admission(inv Invocation) InvocationWorkEnvironmentAdmission {
	return InvocationWorkEnvironmentAdmission{InvocationID: inv.ID, EnvironmentID: info.environment.ID, WorkloadSpecID: info.spec.ID,
		SettingsHash: info.spec.Hash, AppID: inv.AppID, PolicyName: inv.WorkPolicyName, PolicyRevision: inv.WorkPolicyRevision,
		KeyDigest: append([]byte(nil), inv.WorkKeyDigest...), FairnessDigest: append([]byte(nil), inv.WorkFairnessDigest...), FairnessLimit: inv.WorkFairnessLimit}
}

func workEnvironmentDomainKey(appID, policyName, kind string, digest []byte) string {
	return appID + "\x00" + policyName + "\x00" + kind + "\x00" + hex.EncodeToString(digest)
}

func admissionMatchesInvocation(owner InvocationWorkEnvironmentAdmission, inv Invocation) bool {
	return owner.InvocationID == inv.ID && owner.AppID == inv.AppID && owner.PolicyName == inv.WorkPolicyName && owner.PolicyRevision == inv.WorkPolicyRevision &&
		bytes.Equal(owner.KeyDigest, inv.WorkKeyDigest) && bytes.Equal(owner.FairnessDigest, inv.WorkFairnessDigest) && owner.FairnessLimit == inv.WorkFairnessLimit
}

func validateInvocationWorkEnvironmentAdmission(ctx context.Context, store invocationAppReader, inv Invocation, version InvocationVersion) error {
	reader, available := store.(InvocationWorkEnvironmentAdmissionReader)
	var owner InvocationWorkEnvironmentAdmission
	var err error
	if available && inv.ID != "" {
		owner, err = reader.InvocationWorkEnvironmentAdmission(ctx, inv.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	owned := owner.InvocationID != ""
	keyed := inv.WorkPolicyName != "" || len(inv.WorkKeyDigest) != 0 || len(inv.WorkFairnessDigest) != 0
	if !invocationStageScope(version.Scope) {
		if owned {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return nil
	}
	if !keyed && !owned {
		return nil
	}
	if !available || !owned || !stageKeyedInvocationSupported(inv) || !admissionMatchesInvocation(owner, inv) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	info, err := invocationWorkEnvironmentForVersion(ctx, store, inv.AppID, version, inv.WorkPolicyName)
	if err != nil || owner.EnvironmentID != info.environment.ID || owner.WorkloadSpecID != info.spec.ID || owner.SettingsHash != info.spec.Hash ||
		owner.PolicyRevision != info.policy.Revision || inv.WorkFairnessLimit != info.policy.Policy.MaxRunningPerFairnessKey || inv.WorkSequence < 1 {
		return ErrInvocationEnvironmentWorkIsolation
	}
	expires := info.policy.Policy.ExpiresAt(inv.CreatedAt)
	if !sameWorkExpiry(expires, inv.WorkExpiresAt) || inv.DueAt.Before(inv.CreatedAt.Add(info.policy.Policy.Debounce)) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func sameWorkExpiry(expected, actual *time.Time) bool {
	return (expected == nil && actual == nil) || (expected != nil && actual != nil && expected.Equal(*actual))
}
