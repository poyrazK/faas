package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrProjectEnvironmentQueuePreparationUnavailable = fmt.Errorf("environment queue consumer preparation is unavailable: %w", ErrConflict)

// Runtime sets and consumers are private operational identities. A prepared
// set proves a complete projection of pinned definitions, not runnable queue
// isolation. It must never substitute for the dispatch activation proof.
type ProjectEnvironmentQueueRuntimeSet struct {
	ID              string
	AccountID       string
	ProjectID       string
	EnvironmentID   string
	EnvironmentSlug string
	AppID           string
	DeploymentID    string
	WorkloadSpecID  string
	SettingsHash    string
	QueueRevision   int64
	BindingCount    int
	BookHash        string
	State           string
	CreatedAt       time.Time
	Consumers       []ProjectEnvironmentQueueConsumer
}

type ProjectEnvironmentQueueConsumer struct {
	ID, RuntimeSetID, DefinitionHash string
	ProjectEnvironmentQueueDefinition
	CreatedAt time.Time
}

type ProjectEnvironmentQueueConsumerStore interface {
	PrepareProjectEnvironmentQueueConsumers(context.Context, string, string, string) (ProjectEnvironmentQueueRuntimeSet, error)
	ProjectEnvironmentQueueConsumersForDeployment(context.Context, string, string, string) (ProjectEnvironmentQueueRuntimeSet, error)
}

func validateQueuePreparationIDs(accountID, projectID, deploymentID string) error {
	for _, id := range []string{accountID, projectID, deploymentID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

func queuePreparationBook(spec ProjectEnvironmentWorkloadSpec) (ProjectEnvironmentQueueSettings, error) {
	if !invocationStageScope(spec.EnvironmentSlug) || api.ValidateScope(spec.EnvironmentSlug) != nil || spec.Revision < 1 {
		return ProjectEnvironmentQueueSettings{}, ErrConflict
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return ProjectEnvironmentQueueSettings{}, ErrConflict
	}
	if spec.Settings.QueueBindings == nil {
		return ProjectEnvironmentQueueSettings{}, ErrProjectEnvironmentQueueCollectionUnavailable
	}
	settings, err := normalizeWorkloadQueueSettings(spec.Settings)
	if err != nil {
		return ProjectEnvironmentQueueSettings{}, ErrConflict
	}
	return *settings.QueueBindings, nil
}

func queueProjectionHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func newQueueRuntimeSet(spec ProjectEnvironmentWorkloadSpec, deploymentID string, book ProjectEnvironmentQueueSettings) ProjectEnvironmentQueueRuntimeSet {
	set := ProjectEnvironmentQueueRuntimeSet{
		ID: uuid.NewString(), AccountID: spec.AccountID, ProjectID: spec.ProjectID,
		EnvironmentID: spec.EnvironmentID, EnvironmentSlug: spec.EnvironmentSlug,
		AppID: spec.AppID, DeploymentID: deploymentID, WorkloadSpecID: spec.ID, SettingsHash: spec.Hash,
		QueueRevision: book.Revision, BindingCount: len(book.Bindings), BookHash: queueProjectionHash(book),
		State: "prepared", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Consumers: []ProjectEnvironmentQueueConsumer{},
	}
	for _, definition := range book.Bindings {
		set.Consumers = append(set.Consumers, ProjectEnvironmentQueueConsumer{
			ID: uuid.NewString(), RuntimeSetID: set.ID, DefinitionHash: queueProjectionHash(definition),
			ProjectEnvironmentQueueDefinition: definition, CreatedAt: set.CreatedAt,
		})
	}
	return set
}

func validateQueueRuntimeSet(set ProjectEnvironmentQueueRuntimeSet, spec ProjectEnvironmentWorkloadSpec, deploymentID string, book ProjectEnvironmentQueueSettings) error {
	id, err := uuid.Parse(set.ID)
	if err != nil || id == uuid.Nil || id.String() != set.ID || set.State != "prepared" || set.CreatedAt.IsZero() ||
		set.AccountID != spec.AccountID || set.ProjectID != spec.ProjectID || set.EnvironmentID != spec.EnvironmentID ||
		set.EnvironmentSlug != spec.EnvironmentSlug || set.AppID != spec.AppID || set.DeploymentID != deploymentID ||
		set.WorkloadSpecID != spec.ID || set.SettingsHash != spec.Hash || set.QueueRevision != book.Revision ||
		set.BindingCount != len(book.Bindings) || set.BookHash != queueProjectionHash(book) || len(set.Consumers) != len(book.Bindings) {
		return ErrConflict
	}
	seen := map[string]bool{set.ID: true}
	for i, consumer := range set.Consumers {
		parsed, err := uuid.Parse(consumer.ID)
		if err != nil || parsed == uuid.Nil || parsed.String() != consumer.ID || seen[consumer.ID] || consumer.RuntimeSetID != set.ID || consumer.CreatedAt.IsZero() ||
			consumer.DefinitionHash != queueProjectionHash(book.Bindings[i]) || queueProjectionHash(consumer.ProjectEnvironmentQueueDefinition) != consumer.DefinitionHash {
			return ErrConflict
		}
		seen[consumer.ID] = true
	}
	return nil
}

func cloneQueueRuntimeSet(set ProjectEnvironmentQueueRuntimeSet) ProjectEnvironmentQueueRuntimeSet {
	set.Consumers = append([]ProjectEnvironmentQueueConsumer{}, set.Consumers...)
	for i := range set.Consumers {
		set.Consumers[i].RetryPolicyJSON = append(json.RawMessage(nil), set.Consumers[i].RetryPolicyJSON...)
	}
	return set
}

func decodeQueueConsumerDefinition(raw []byte) (ProjectEnvironmentQueueDefinition, error) {
	var definition ProjectEnvironmentQueueDefinition
	err := decodeQueueProjectionJSON(raw, &definition)
	return definition, err
}

func decodeQueueProjectionJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrConflict
	}
	return nil
}
