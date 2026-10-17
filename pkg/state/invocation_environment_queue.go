package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Queue admission is private operational ownership, not clone configuration.
// A prepared consumer alone does not enable public ingress or dispatch.
type InvocationEnvironmentQueueAdmission struct {
	InvocationID, EnvironmentID, AccountID, AppID          string
	ConsumerID, RuntimeSetID, DeploymentID, WorkloadSpecID string
	PinHash, SettingsHash, DefinitionHash, QueueName       string
	AdmittedAt                                             time.Time
}

type InvocationEnvironmentQueueAdmissionReader interface {
	InvocationEnvironmentQueueAdmission(context.Context, string) (InvocationEnvironmentQueueAdmission, error)
}

type ProjectEnvironmentQueueInvocationStore interface {
	EnqueueProjectEnvironmentQueueInvocation(context.Context, string, string, string, string, Invocation) (Invocation, error)
}

func stageQueueInvocationShape(inv Invocation) bool {
	return inv.Source == InvocationQueue && environmentQueueNameRE.MatchString(inv.QueueName) && inv.CronID == nil && inv.AckURL == "" &&
		inv.OnSuccessDestinationID == "" && inv.OnFailureDestinationID == "" && inv.WorkPolicyName == "" && inv.WorkPolicyRevision == 0 &&
		len(inv.WorkKeyDigest) == 0 && len(inv.WorkFairnessDigest) == 0 && inv.WorkFairnessLimit == 0 && inv.WorkSequence == 0 && inv.WorkExpiresAt == nil
}

func queueConsumerByName(set ProjectEnvironmentQueueRuntimeSet, name string) (ProjectEnvironmentQueueConsumer, error) {
	for _, consumer := range set.Consumers {
		if consumer.Name == name && consumer.Enabled {
			return consumer, nil
		}
	}
	return ProjectEnvironmentQueueConsumer{}, ErrNotFound
}

func prepareEnvironmentQueueInvocation(ctx context.Context, store invocationAppReader, set ProjectEnvironmentQueueRuntimeSet, name string, inv Invocation) (Invocation, error) {
	consumer, err := queueConsumerByName(set, name)
	if err != nil {
		return Invocation{}, err
	}
	if (inv.AppID != "" && inv.AppID != set.AppID) || (inv.AccountID != "" && inv.AccountID != set.AccountID) ||
		(inv.EnvironmentID != "" && inv.EnvironmentID != set.EnvironmentID) || (inv.QueueName != "" && inv.QueueName != consumer.QueueName) ||
		(inv.Source != "" && inv.Source != InvocationQueue) {
		return Invocation{}, ErrInvalidArgument
	}
	inv.AppID, inv.AccountID, inv.QueueName, inv.Source = set.AppID, set.AccountID, consumer.QueueName, InvocationQueue
	if !stageQueueInvocationShape(inv) || (inv.State != "" && inv.State != InvocationPending) || inv.InstanceID != "" || inv.Attempts != 0 || inv.QuotaReserved ||
		inv.LeaseExpiresAt != nil || inv.ReceivedAt != nil || inv.CompletedAt != nil || inv.Outcome != nil || len(inv.Result) != 0 || inv.LastError != "" ||
		!inv.CreatedAt.IsZero() || inv.LastReplayedAt != nil {
		return Invocation{}, ErrInvalidArgument
	}
	if inv.ID != "" {
		id, err := uuid.Parse(inv.ID)
		if err != nil || id == uuid.Nil {
			return Invocation{}, ErrInvalidArgument
		}
		inv.ID = id.String()
	}
	inv.Payload, err = jsonOrEmpty(inv.Payload)
	if err != nil {
		return Invocation{}, ErrInvalidArgument
	}
	if len(inv.RetryPolicyJSON) > 0 {
		definition := consumer.ProjectEnvironmentQueueDefinition
		definition.RetryPolicyJSON = inv.RetryPolicyJSON
		normalized, err := normalizeEnvironmentQueues([]ProjectEnvironmentQueueDefinition{definition})
		if err != nil || !bytes.Equal(normalized[0].RetryPolicyJSON, consumer.RetryPolicyJSON) {
			return Invocation{}, ErrConflict
		}
	}
	inv.RetryPolicyJSON = append(json.RawMessage(nil), consumer.RetryPolicyJSON...)
	var headers map[string]string
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return Invocation{}, ErrInvalidArgument
	}
	if headers == nil {
		headers = map[string]string{}
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return Invocation{}, err
	}
	if revision == "" && release == "" {
		headers[api.RevisionHeader] = set.DeploymentID
	}
	inv.Headers, _ = json.Marshal(headers)
	// Authenticate the selected pin before the private row/proof exists.
	base := inv
	base.ID, base.EnvironmentID, base.QueueName, base.Source = "", "", "", InvocationAsyncInvoke
	prepared, version, err := ResolveInvocationVersionForEnvironment(ctx, store, base, set.EnvironmentSlug)
	if err != nil {
		return Invocation{}, err
	}
	if version.DeploymentID != set.DeploymentID {
		return Invocation{}, ErrConflict
	}
	inv.Headers, inv.EnvironmentID, inv.State = prepared.Headers, set.EnvironmentID, InvocationPending
	inv.DeploymentScope = prepared.DeploymentScope
	inv.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if inv.DueAt.IsZero() {
		inv.DueAt = inv.CreatedAt
	}
	return inv, nil
}

func queueAdmissionForInvocation(set ProjectEnvironmentQueueRuntimeSet, consumer ProjectEnvironmentQueueConsumer, inv Invocation) InvocationEnvironmentQueueAdmission {
	return InvocationEnvironmentQueueAdmission{InvocationID: inv.ID, EnvironmentID: set.EnvironmentID, AccountID: set.AccountID, AppID: set.AppID,
		ConsumerID: consumer.ID, RuntimeSetID: set.ID, DeploymentID: set.DeploymentID, WorkloadSpecID: set.WorkloadSpecID,
		PinHash: queueAdmissionPinHash(inv), SettingsHash: set.SettingsHash, DefinitionHash: consumer.DefinitionHash, QueueName: consumer.QueueName, AdmittedAt: inv.CreatedAt}
}

func queueRetryPolicyMatches(consumer ProjectEnvironmentQueueConsumer, raw json.RawMessage) bool {
	definition := consumer.ProjectEnvironmentQueueDefinition
	definition.RetryPolicyJSON = raw
	normalized, err := normalizeEnvironmentQueues([]ProjectEnvironmentQueueDefinition{definition})
	return err == nil && bytes.Equal(normalized[0].RetryPolicyJSON, consumer.RetryPolicyJSON)
}

func queueAdmissionPinHash(inv Invocation) string {
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return ""
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil || (revision == "" && release == "") {
		return ""
	}
	return queueProjectionHash([2]string{revision, release})
}

func queueOwnerMatchesEnvelope(owner InvocationEnvironmentQueueAdmission, inv Invocation) bool {
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return false
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil || (revision == "" && release == "") || (revision != "" && revision != owner.DeploymentID) {
		return false
	}
	return owner.PinHash != "" && owner.PinHash == queueAdmissionPinHash(inv) && stageQueueInvocationShape(inv) && inv.ID == owner.InvocationID && inv.EnvironmentID == owner.EnvironmentID && inv.AccountID == owner.AccountID &&
		inv.AppID == owner.AppID && inv.QueueName == owner.QueueName && !owner.AdmittedAt.IsZero() && inv.CreatedAt.Equal(owner.AdmittedAt)
}

func validateQueueAdmission(owner InvocationEnvironmentQueueAdmission, inv Invocation, set ProjectEnvironmentQueueRuntimeSet) (ProjectEnvironmentQueueConsumer, error) {
	if !queueOwnerMatchesEnvelope(owner, inv) ||
		owner.EnvironmentID != set.EnvironmentID || owner.AccountID != set.AccountID || owner.AppID != set.AppID || owner.RuntimeSetID != set.ID ||
		owner.DeploymentID != set.DeploymentID || owner.WorkloadSpecID != set.WorkloadSpecID || owner.SettingsHash != set.SettingsHash {
		return ProjectEnvironmentQueueConsumer{}, ErrInvocationEnvironmentWorkIsolation
	}
	for _, consumer := range set.Consumers {
		if consumer.ID == owner.ConsumerID && consumer.Enabled && consumer.QueueName == owner.QueueName && consumer.DefinitionHash == owner.DefinitionHash &&
			queueRetryPolicyMatches(consumer, inv.RetryPolicyJSON) {
			return consumer, nil
		}
	}
	return ProjectEnvironmentQueueConsumer{}, ErrInvocationEnvironmentWorkIsolation
}

func validateInvocationEnvironmentQueueAdmission(ctx context.Context, store invocationAppReader, inv Invocation, version InvocationVersion) error {
	reader, ok := store.(InvocationEnvironmentQueueAdmissionReader)
	var owner InvocationEnvironmentQueueAdmission
	if ok && inv.ID != "" {
		var err error
		owner, err = reader.InvocationEnvironmentQueueAdmission(ctx, inv.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if owner.InvocationID == "" {
		if bound, err := validateBoundQueueEnvironment(ctx, store, inv, version.Scope); bound || err != nil {
			return err
		}
		if invocationStageScope(version.Scope) && inv.Source == InvocationQueue && (version.DeploymentID != "" || inv.EnvironmentID != "") {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return nil
	}
	consumers, ok := store.(interface {
		ProjectEnvironmentQueueConsumersForDeployment(context.Context, string, string, string) (ProjectEnvironmentQueueRuntimeSet, error)
	})
	if !ok || !invocationStageScope(version.Scope) || version.DeploymentID != owner.DeploymentID {
		return ErrInvocationEnvironmentWorkIsolation
	}
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return err
	}
	set, err := consumers.ProjectEnvironmentQueueConsumersForDeployment(ctx, owner.AccountID, app.ProjectID, owner.DeploymentID)
	if err != nil {
		return ErrInvocationEnvironmentWorkIsolation
	}
	_, err = validateQueueAdmission(owner, inv, set)
	return err
}
