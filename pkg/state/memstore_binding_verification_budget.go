package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
)

func (m *MemStore) verificationBudgetLocked(account string, r verificationRunRecord) (api.BindingVerificationBudget, error) {
	root, limit, runs := r.Report.ID, r.Report.MaxConcurrency, []verificationRunRecord{r}
	if r.Report.GraphRunID != "" {
		graph, ok := m.graphVerificationRuns[r.Report.GraphRunID]
		if !ok || graph.AccountID != account {
			return api.BindingVerificationBudget{}, ErrNotFound
		}
		root, limit, runs = graph.Report.ID, graph.Report.MaxConcurrency, nil
		for _, name := range graphVerificationMemberNames(graph.Report) {
			member := graph.Report.Members[name]
			child, err := m.verificationRunLocked(account, member.Policy.AppID, member.ID)
			if err != nil {
				return api.BindingVerificationBudget{}, err
			}
			if child.Report.GraphRunID != root {
				return api.BindingVerificationBudget{}, ErrConflict
			}
			runs = append(runs, child)
		}
	}
	out, refs, err := verificationBudgetSelection(root, limit, runs)
	if err != nil {
		return out, err
	}
	for key, app := range refs {
		kind, id, _ := strings.Cut(key, "/")
		if kind == "task" {
			task, ok := m.appTasks[id]
			if !ok {
				continue
			}
			if task.AccountID != account || task.AppID != app {
				return out, ErrConflict
			}
			if !api.AppTaskStatus(task.Status).Terminal() {
				out.ActiveResources++
			}
		} else {
			invocation, ok := m.invocations[id]
			if !ok {
				continue
			}
			if invocation.AccountID != account || invocation.AppID != app {
				return out, ErrConflict
			}
			if invocation.State != InvocationCompleted && invocation.State != InvocationFailed && invocation.State != InvocationCancelled && invocation.State != InvocationExpired && invocation.State != InvocationSuperseded && invocation.State != InvocationDeadLetter {
				out.ActiveResources++
			}
		}
	}
	out.Full = out.ActiveResources >= out.MaxConcurrency
	return out, nil
}
func (m *MemStore) BindingVerificationRunBudget(_ context.Context, account, app, id string) (api.BindingVerificationBudget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.verificationRunLocked(account, app, id)
	if err != nil {
		return api.BindingVerificationBudget{}, err
	}
	return m.verificationBudgetLocked(account, r)
}
