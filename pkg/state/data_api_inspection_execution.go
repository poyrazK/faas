package state

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

// DataAPIInspectionExecution is scheduler-owned evidence of the protocol
// exchange and exact retained output. It is not binding or deployment proof.
type DataAPIInspectionExecution struct {
	TaskID, AppID, DeploymentID string
	ProtocolVersion             uint16
	StdoutSHA256                [32]byte
	FinishedAt                  time.Time
}

type DataAPIInspectionExecutionStore interface {
	DataAPIInspectionExecution(context.Context, string, string, string) (DataAPIInspectionExecution, error)
}

var _ DataAPIInspectionExecutionStore = (*MemStore)(nil)
var _ DataAPIInspectionExecutionStore = (*PgStore)(nil)

func validateDataAPIInspectionExecution(task AppTask, params CompleteAppTaskParams) error {
	if params.DataAPIInspectionProtocol == 0 {
		return nil
	}
	if params.DataAPIInspectionProtocol != apptaskproto.DataAPIInspectionVersion || task.Kind != AppTaskKindManual || task.Status != AppTaskRunning || task.FailureRules != nil || !dataapiinspectioncommand.Valid(task.Command, task.CommandShell) || params.Status != AppTaskSucceeded || params.OutputTruncated || params.ExitCode == nil || *params.ExitCode != 0 || params.StdoutTail == "" {
		return ErrAppTaskInvalid
	}
	return nil
}

func (m *MemStore) DataAPIInspectionExecution(_ context.Context, accountID, appID, taskID string) (DataAPIInspectionExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	execution, found := m.dataAPIInspectionExecutions[taskID]
	if !ok || !found || task.AccountID != accountID || task.AppID != appID || task.Status != AppTaskSucceeded || task.OutputTruncated || task.FinishedAt == nil || execution.TaskID != task.ID || execution.AppID != task.AppID || execution.DeploymentID != task.DeploymentID || execution.ProtocolVersion != apptaskproto.DataAPIInspectionVersion || !execution.FinishedAt.Equal(*task.FinishedAt) || !dataapiinspectioncommand.Valid(task.Command, task.CommandShell) {
		return DataAPIInspectionExecution{}, ErrNotFound
	}
	digest := sha256.Sum256([]byte(task.StdoutTail))
	if subtle.ConstantTimeCompare(digest[:], execution.StdoutSHA256[:]) != 1 {
		return DataAPIInspectionExecution{}, ErrNotFound
	}
	return execution, nil
}

func (s *PgStore) DataAPIInspectionExecution(ctx context.Context, accountID, appID, taskID string) (DataAPIInspectionExecution, error) {
	var result DataAPIInspectionExecution
	var digest []byte
	var stdout string
	var command []string
	var shell bool
	var version int16
	err := s.pool.QueryRow(ctx, `SELECT t.id::text,t.app_id::text,t.deployment_id::text,e.protocol_version,e.stdout_sha256,e.finished_at,t.stdout_tail,t.command,t.command_shell
		FROM app_tasks t JOIN data_api_inspection_executions e ON e.task_id=t.id
		WHERE t.account_id=$1 AND t.app_id=$2 AND t.id=$3 AND t.status='succeeded' AND NOT t.output_truncated AND t.finished_at=e.finished_at`, accountID, appID, taskID).Scan(&result.TaskID, &result.AppID, &result.DeploymentID, &version, &digest, &result.FinishedAt, &stdout, &command, &shell)
	if errors.Is(err, pgx.ErrNoRows) {
		return DataAPIInspectionExecution{}, ErrNotFound
	}
	if err != nil {
		return DataAPIInspectionExecution{}, mapErr(err)
	}
	computed := sha256.Sum256([]byte(stdout))
	if version != int16(apptaskproto.DataAPIInspectionVersion) || len(digest) != len(computed) || subtle.ConstantTimeCompare(digest, computed[:]) != 1 || !dataapiinspectioncommand.Valid(command, shell) {
		return DataAPIInspectionExecution{}, ErrNotFound
	}
	result.ProtocolVersion, result.StdoutSHA256 = uint16(version), computed
	return result, nil
}
