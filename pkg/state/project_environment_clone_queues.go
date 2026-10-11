package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Source IDs are private remapping inputs for legacy production rows. Scoped
// settings contain logical definitions, never production consumer identities.
type ProjectEnvironmentCloneQueueBinding struct {
	SourceID string `json:"source_id,omitempty"`
	ProjectEnvironmentQueueDefinition
}

type ProjectEnvironmentCloneQueueDefinitions struct {
	Version          int                                   `json:"version"`
	AppID            string                                `json:"app_id"`
	SourceScope      string                                `json:"source_scope"`
	EnvironmentOwned bool                                  `json:"environment_owned"`
	Bindings         []ProjectEnvironmentCloneQueueBinding `json:"bindings"`
}

type ProjectEnvironmentCloneQueueCapture struct {
	OperationID, Hash string
	Definitions       ProjectEnvironmentCloneQueueDefinitions
}

type ProjectEnvironmentCloneQueueCaptureStore interface {
	ProjectEnvironmentCloneQueuesForLease(context.Context, ProjectEnvironmentCloneLease) ([]ProjectEnvironmentCloneQueueCapture, error)
}

func normalizeCloneQueues(definitions ProjectEnvironmentCloneQueueDefinitions) (ProjectEnvironmentCloneQueueDefinitions, error) {
	if definitions.Version != 1 || !validCloneCredentialSourceID(definitions.AppID) || api.ValidateScope(definitions.SourceScope) != nil {
		return definitions, ErrConflict
	}
	bindings := make([]ProjectEnvironmentQueueDefinition, 0, len(definitions.Bindings))
	ids, names := map[string]bool{}, map[string]string{}
	for _, binding := range definitions.Bindings {
		if (definitions.EnvironmentOwned && binding.SourceID != "") || (!definitions.EnvironmentOwned && (!validCloneCredentialSourceID(binding.SourceID) || ids[binding.SourceID])) {
			return definitions, ErrConflict
		}
		ids[binding.SourceID] = true
		names[binding.Name] = binding.SourceID
		bindings = append(bindings, binding.ProjectEnvironmentQueueDefinition)
	}
	bindings, err := normalizeEnvironmentQueues(bindings)
	if err != nil {
		return definitions, ErrConflict
	}
	definitions.Bindings = make([]ProjectEnvironmentCloneQueueBinding, 0, len(bindings))
	for _, binding := range bindings {
		definitions.Bindings = append(definitions.Bindings, ProjectEnvironmentCloneQueueBinding{SourceID: names[binding.Name], ProjectEnvironmentQueueDefinition: binding})
	}
	return definitions, nil
}

func cloneQueueSettings(definitions ProjectEnvironmentCloneQueueDefinitions) []ProjectEnvironmentQueueDefinition {
	bindings := make([]ProjectEnvironmentQueueDefinition, 0, len(definitions.Bindings))
	for _, binding := range definitions.Bindings {
		bindings = append(bindings, binding.ProjectEnvironmentQueueDefinition)
	}
	return bindings
}

func captureCloneQueuesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, snapshot *projectCloneWorkloadSnapshot) error {
	definitions := ProjectEnvironmentCloneQueueDefinitions{Version: 1, AppID: snapshot.Artifact.AppID, SourceScope: snapshot.Artifact.Scope, Bindings: []ProjectEnvironmentCloneQueueBinding{}}
	if snapshot.Settings.QueueBindings != nil {
		definitions.EnvironmentOwned = true
		for _, binding := range snapshot.Settings.QueueBindings.Bindings {
			definitions.Bindings = append(definitions.Bindings, ProjectEnvironmentCloneQueueBinding{ProjectEnvironmentQueueDefinition: binding})
		}
	} else {
		row, err := sqlc.New().CaptureProjectEnvironmentCloneQueues(ctx, tx, sqlc.CaptureProjectEnvironmentCloneQueuesParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(snapshot.Artifact.AppID), SourceScope: snapshot.Artifact.Scope})
		if err != nil {
			return mapErr(err)
		}
		if json.Unmarshal(row.Definitions, &definitions) != nil || row.OwnershipViolations != 0 {
			return ErrConflict
		}
		if invocationStageScope(snapshot.Artifact.Scope) && len(definitions.Bindings) > 0 {
			return ErrProjectEnvironmentQueueCollectionUnavailable
		}
	}
	definitions, err := normalizeCloneQueues(definitions)
	if err != nil {
		return err
	}
	if snapshot.Settings.QueueBindings == nil {
		snapshot.Settings.QueueBindings = &ProjectEnvironmentQueueSettings{Revision: 1, Bindings: cloneQueueSettings(definitions)}
	}
	snapshot.Settings, err = normalizeWorkloadQueueSettings(snapshot.Settings)
	if err != nil {
		return fmt.Errorf("clone workload %q queue settings: %w", snapshot.WorkloadSlug, err)
	}
	snapshot.Policies.Queues = &definitions
	return nil
}

// Read the committed root under the current worker lease. Later production
// edits never repopulate a missing or explicitly empty captured collection.
func (s *PgStore) ProjectEnvironmentCloneQueuesForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) ([]ProjectEnvironmentCloneQueueCapture, error) {
	if !validCloneLeaseIdentity(lease) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return nil, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, err
	}
	root, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return nil, err
	}
	if root.Version != 1 {
		return nil, ErrProjectEnvironmentClonePolicyCaptureUnavailable
	}
	captures := make([]ProjectEnvironmentCloneQueueCapture, 0, len(records))
	for _, record := range records {
		if record.snapshot.Policies == nil || record.snapshot.Policies.Queues == nil {
			return nil, ErrProjectEnvironmentQueueCollectionUnavailable
		}
		definitions, err := normalizeCloneQueues(*record.snapshot.Policies.Queues)
		if err != nil || definitions.AppID != record.AppID || definitions.SourceScope != record.SourceScope {
			return nil, ErrConflict
		}
		raw, err := json.Marshal(definitions)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(raw)
		captures = append(captures, ProjectEnvironmentCloneQueueCapture{OperationID: op.ID, Hash: hex.EncodeToString(hash[:]), Definitions: definitions})
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	return captures, tx.Commit(ctx)
}

var _ ProjectEnvironmentCloneQueueCaptureStore = (*PgStore)(nil)
