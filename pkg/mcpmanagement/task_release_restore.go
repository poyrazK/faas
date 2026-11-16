package mcpmanagement

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type applyTaskRestoreArgs struct {
	IntentID            string `json:"intent_id"`
	OperationID         string `json:"operation_id"`
	HandoverID          string `json:"handover_id"`
	QualificationSHA256 string `json:"qualification_sha256"`
	AllowToolExecution  bool   `json:"allow_tool_execution" jsonschema:"Explicit consent to the one-time owner-approved web restore"`
}

func (a applyTaskRestoreArgs) request() api.TaskToolRecoveryRestoreRequest {
	return api.TaskToolRecoveryRestoreRequest{OperationID: a.OperationID, HandoverID: a.HandoverID, QualificationSHA256: a.QualificationSHA256}
}
func (m *management) applyTaskRestore(ctx context.Context, args applyTaskRestoreArgs) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites || !args.AllowToolExecution || !toolpublication.ValidID(args.IntentID) {
		return nil, invalid("invalid_argument", "Task restore needs exact identities and explicit execution consent")
	}
	record, err := m.client.GetTaskToolReleaseIntent(ctx, args.IntentID)
	if err != nil {
		return nil, err
	}
	if _, err = validateTaskIntentReceipt(record, args.IntentID); err != nil {
		return nil, err
	}
	if !tasktoolrelease.RestoreIdentity(record, args.request()) {
		return nil, invalid("execution_not_authorized", "no matching owner recovery approval")
	}
	h := record.Execution.Recovery
	if h.Result != nil {
		return record, nil
	}
	if h.State != "settled" || h.Decision != "approved" || !time.Now().Before(h.Qualification.ExpiresAt) {
		return nil, invalid("execution_not_authorized", "an unexpired separately approved settled recovery is required")
	}
	key := "restore:" + h.ID + ":" + args.QualificationSHA256
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.taskExecutions[key] {
		return record, nil
	}
	select {
	case toolPublishSlots <- struct{}{}:
	default:
		return nil, invalid("publication_capacity", "release workers are busy; inspect the same recovery operation")
	}
	work, cancel := context.WithDeadline(context.WithoutCancel(ctx), h.Qualification.ExpiresAt)
	work, finish, err := m.lifecycle.enter(work, false)
	if err != nil {
		cancel()
		<-toolPublishSlots
		return nil, err
	}
	work, finishGrant, err := m.grantLifecycle.enter(work, false)
	if err != nil {
		finish()
		cancel()
		<-toolPublishSlots
		return nil, err
	}
	if m.taskExecutions == nil {
		m.taskExecutions = map[string]bool{}
	}
	m.taskExecutions[key] = true
	go func() {
		defer cancel()
		defer finish()
		defer finishGrant()
		defer func() { <-toolPublishSlots; m.mu.Lock(); delete(m.taskExecutions, key); m.mu.Unlock() }()
		_, _ = m.runCLI(work, []string{"mcp", "server", "tasks", "execution", "restore", "--id", args.IntentID, "--operation-id", args.OperationID, "--handover-id", args.HandoverID, "--qualification-sha256", args.QualificationSHA256, "--allow-tool-execution"}, nil)
	}()
	return record, nil
}
func bindTaskRestoreReceipt(args any, data json.RawMessage) (json.RawMessage, error) {
	var in applyTaskRestoreArgs
	raw, _ := json.Marshal(args)
	var record api.TaskToolReleaseIntentRecord
	if json.Unmarshal(raw, &in) != nil || json.Unmarshal(data, &record) != nil {
		return nil, invalid("invalid_response", "invalid Task recovery receipt")
	}
	if _, err := validateTaskIntentReceipt(record, in.IntentID); err != nil {
		return nil, err
	}
	if !tasktoolrelease.RestoreIdentity(record, in.request()) {
		return nil, invalid("invalid_response", "Task restore identity differs from its request")
	}
	return data, nil
}
