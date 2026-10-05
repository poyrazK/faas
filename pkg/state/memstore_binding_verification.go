package state

import (
	"context"
	"sort"
)

var _ BindingVerificationStore = (*MemStore)(nil)

func (m *MemStore) ListBindingVerificationTasks(_ context.Context, accountID, appID string, kinds []string, selections ...BindingVerificationSelection) ([]BindingVerificationTask, error) {
	selection, err := resolveBindingVerificationSelection(selections)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	allowed := map[string]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	latest := map[string]AppTask{}
	for _, task := range m.appTasks {
		pin := task.BindingVerification
		if task.AccountID != accountID || task.AppID != appID || pin == nil || !allowed[pin.Type] {
			continue
		}
		matches := selection.DeploymentID != "" && sameDeploymentID(task.DeploymentID, selection.DeploymentID)
		if selection.DeploymentID != "" && !selection.AllowFallback && !matches {
			continue
		}
		key := pin.Type + "\x00" + pin.Binding + "\x00" + task.DeploymentScope
		previous, exists := latest[key]
		previousMatches := selection.DeploymentID != "" && sameDeploymentID(previous.DeploymentID, selection.DeploymentID)
		if !exists || matches && !previousMatches || matches == previousMatches && (task.CreatedAt.After(previous.CreatedAt) || task.CreatedAt.Equal(previous.CreatedAt) && task.ID > previous.ID) {
			latest[key] = task
		}
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]BindingVerificationTask, 0, len(keys))
	for _, key := range keys {
		task := latest[key]
		output := task.StdoutTail
		truncated := task.OutputTruncated || len(output) > 4096
		if len(output) > 4096 {
			output = ""
		}
		var exit *int
		if task.ExitCode != nil {
			value := *task.ExitCode
			exit = &value
		}
		items = append(items, BindingVerificationTask{
			Pin: *cloneBindingVerificationPin(task.BindingVerification), DeploymentID: task.DeploymentID,
			Scope: task.DeploymentScope, Status: string(task.Status), Stdout: output,
			CreatedAt: task.CreatedAt, FinishedAt: cloneAppTaskTimePtr(task.FinishedAt), OutputTruncated: truncated, ExitCode: exit,
		})
	}
	return items, nil
}
