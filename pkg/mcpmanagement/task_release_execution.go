package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type applyTaskExecutionArgs struct {
	IntentID           string `json:"intent_id"`
	OperationID        string `json:"operation_id" jsonschema:"Exact separately owner-authorized Task execution identity"`
	AllowToolExecution bool   `json:"allow_tool_execution" jsonschema:"Explicit consent to the reviewed code, database migrations and native release effects"`
}

func (m *management) getTaskExecution(ctx context.Context, args taskReleaseIntentArgs) (any, error) {
	return m.getTaskReleaseIntent(ctx, args)
}

func (m *management) applyTaskExecution(ctx context.Context, args applyTaskExecutionArgs) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites || !args.AllowToolExecution || !toolpublication.ValidID(args.IntentID) || !toolpublication.ValidID(args.OperationID) {
		return nil, invalid("invalid_argument", "Task apply requires write consent, explicit execution consent and exact intent/operation identities")
	}
	retained, err := m.client.GetTaskToolReleaseIntent(ctx, args.IntentID)
	if err != nil {
		return nil, err
	}
	if _, err = validateTaskIntentReceipt(retained, args.IntentID); err != nil {
		return nil, err
	}
	if retained.Execution == nil || retained.Execution.OperationID != args.OperationID {
		return nil, invalid("execution_not_authorized", "a separate matching owner execution authorization is required")
	}
	if retained.Execution.State != "authorized" {
		return retained, nil
	}
	if retained.Status != "approved" || !time.Now().Before(retained.ExpiresAt) {
		return nil, invalid("execution_not_authorized", "the owner-approved review has expired or changed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.taskExecutions[args.IntentID] {
		return retained, nil
	}
	select {
	case toolPublishSlots <- struct{}{}:
	default:
		return nil, invalid("publication_capacity", "release workers are busy; inspect and retry the same unstarted operation")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TaskToolReleaseApprovalTTL)
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
	m.taskExecutions[args.IntentID] = true
	go func() {
		defer cancel()
		defer finish()
		defer finishGrant()
		defer func() { <-toolPublishSlots; m.mu.Lock(); delete(m.taskExecutions, args.IntentID); m.mu.Unlock() }()
		// CLI and child output may contain local diagnostics. Poll only the
		// native journal; never forward failed-command output or rerun it.
		_, _ = m.runCLI(work, []string{"mcp", "server", "tasks", "execution", "apply", "--id", args.IntentID, "--operation-id", args.OperationID, "--allow-tool-execution"}, nil)
	}()
	return retained, nil
}
