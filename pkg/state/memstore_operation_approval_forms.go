// adr: 20261019001500
package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type memOperationApprovalForm struct {
	accountID string
	form      api.OperationApprovalForm
}

func (m *MemStore) operationApprovalFormLocked(accountID string, def OperationDefinition, step string) *api.OperationApprovalForm {
	if !api.OperationApprovalInputsEnabled(accountID) {
		return nil
	}
	for _, f := range m.operationMemoryLocked().approvalForms {
		if f.form.AppID == def.AppID && f.form.Scope == def.Scope && f.form.Operation == def.Spec.Name && f.form.Step == step {
			form := f.form
			form.InputSchema = append([]byte(nil), f.form.InputSchema...)
			return &form
		}
	}
	return nil
}

func (m *MemStore) PutOperationApprovalForm(_ context.Context, in OperationApprovalFormInput) (api.OperationApprovalForm, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.accounts[in.AccountID]
	if !ok {
		return api.OperationApprovalForm{}, false, ErrNotFound
	}
	if app, ok := m.apps[in.AppID]; !ok || app.AccountID != in.AccountID {
		return api.OperationApprovalForm{}, false, ErrNotFound
	}
	limits := api.MustLimitsFor(acct.Plan).Operations
	scope, schema, err := validateOperationApprovalFormRequest(in.Request, limits)
	if err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	data := m.operationMemoryLocked()
	if data.approvalForms == nil {
		data.approvalForms = map[string]memOperationApprovalForm{}
	}
	now := in.Now.UTC().Truncate(time.Microsecond)
	count := 0
	for id, f := range data.approvalForms {
		if f.form.AppID != in.AppID {
			continue
		}
		count++
		if f.form.Scope == scope && f.form.Operation == in.Request.Operation && f.form.Step == in.Request.Step {
			f.form.InputSchema, f.form.Required, f.form.UpdatedAt = schema, in.Request.Required, now
			data.approvalForms[id] = f
			return f.form, false, nil
		}
	}
	if count >= limits.ApprovalFormsPerApp {
		return api.OperationApprovalForm{}, false, NewOperationLimitError("approval_forms_per_app", int64(limits.ApprovalFormsPerApp), int64(count)+1)
	}
	form := api.OperationApprovalForm{ID: uuid.NewString(), AppID: in.AppID, Scope: scope, Operation: in.Request.Operation, Step: in.Request.Step,
		InputSchema: schema, Required: in.Request.Required, CreatedAt: now, UpdatedAt: now}
	data.approvalForms[form.ID] = memOperationApprovalForm{accountID: in.AccountID, form: form}
	return form, true, nil
}

func (m *MemStore) OperationApprovalForms(_ context.Context, accountID, appID string) ([]api.OperationApprovalForm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.OperationApprovalForm{}
	for _, f := range m.operationMemoryLocked().approvalForms {
		if f.accountID == accountID && f.form.AppID == appID {
			out = append(out, f.form)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Scope+"\x00"+out[i].Operation+"\x00"+out[i].Step < out[j].Scope+"\x00"+out[j].Operation+"\x00"+out[j].Step
	})
	return out, nil
}

func (m *MemStore) DeleteOperationApprovalForm(_ context.Context, accountID, appID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	f, ok := data.approvalForms[id]
	if !ok || f.accountID != accountID || f.form.AppID != appID {
		return ErrNotFound
	}
	delete(data.approvalForms, id)
	return nil
}

func (m *MemStore) OperationApprovalFormView(_ context.Context, accountID, tenantID, operationID, step string) (api.OperationApprovalFormView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[operationID]
	if !ok || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) || op.WorkflowRunID == "" {
		return api.OperationApprovalFormView{}, ErrNotFound
	}
	def := data.definitions[op.DefinitionID]
	approval, err := approvalForCaller(def, step, tenantID)
	if err != nil {
		return api.OperationApprovalFormView{}, err
	}
	form := m.operationApprovalFormLocked(op.AccountID, def, step)
	if form == nil {
		return api.OperationApprovalFormView{}, ErrNotFound
	}
	var payload []byte
	eventID := api.WorkflowCallbackID(op.WorkflowRunID, step)
	for _, event := range m.workflowEvents[op.WorkflowRunID] {
		if event.ID == eventID {
			payload = event.Payload
		}
	}
	return operationApprovalFormView(approval, *form, payload)
}
