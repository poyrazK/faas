// adr: 20261019001500
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// OperationApprovalFormInput sets one approval form on an app.
type OperationApprovalFormInput struct {
	AccountID string
	AppID     string
	Request   api.OperationApprovalFormRequest
	Now       time.Time
}

// OperationApprovalFormStore owns approval input forms.
type OperationApprovalFormStore interface {
	// PutOperationApprovalForm creates or replaces the form for its app,
	// scope, Operation name and step.
	PutOperationApprovalForm(ctx context.Context, in OperationApprovalFormInput) (api.OperationApprovalForm, bool, error)
	OperationApprovalForms(ctx context.Context, accountID, appID string) ([]api.OperationApprovalForm, error)
	DeleteOperationApprovalForm(ctx context.Context, accountID, appID, id string) error
	// OperationApprovalFormView returns one Operation's approval step with
	// its form and recorded decision. tenantID scopes the Operation to a
	// customer, who sees only steps it approves.
	OperationApprovalFormView(ctx context.Context, accountID, tenantID, operationID, step string) (api.OperationApprovalFormView, error)
}

// validateOperationApprovalFormRequest checks a form and returns its scope
// and canonical schema.
func validateOperationApprovalFormRequest(req api.OperationApprovalFormRequest, limits api.OperationPlanLimits) (string, json.RawMessage, error) {
	scope := req.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if api.ValidateScope(scope) != nil || !operationConcurrencyName.MatchString(req.Operation) || !operationApprovalStep.MatchString(req.Step) {
		return "", nil, fmt.Errorf("%w: operation, step and scope must name an Operation's approval step", ErrInvalidArgument)
	}
	_, canonical, err := operations.CompileApprovalInputSchema(req.InputSchema, limits.SchemaBytes)
	if err != nil {
		return "", nil, fmt.Errorf("%w: input_schema: %w", ErrInvalidArgument, err)
	}
	if canonical[0] != '{' {
		return "", nil, fmt.Errorf("%w: input_schema must be a JSON object", ErrInvalidArgument)
	}
	return scope, canonical, nil
}

// checkOperationApprovalInput applies the step's form to a new decision.
// form is nil when the step has none or the rollout is closed.
func checkOperationApprovalInput(form *api.OperationApprovalForm, in OperationApprovalInput, limits api.OperationPlanLimits) error {
	switch {
	case form == nil && len(in.Input) > 0:
		return fmt.Errorf("%w: this approval takes no input", ErrInvalidArgument)
	case form == nil:
		return nil
	case len(in.Input) == 0 && form.Required && in.Decision == api.OperationApprovalApproved:
		return fmt.Errorf("%w: this approval requires input", ErrInvalidArgument)
	case len(in.Input) == 0:
		return nil
	}
	schema, _, err := operations.CompileApprovalInputSchema(form.InputSchema, limits.SchemaBytes)
	if err != nil {
		return fmt.Errorf("%w: approval form: %w", ErrInvalidArgument, err)
	}
	if err := schema.Validate(in.Input, api.OperationApprovalInputMaxBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return nil
}

// operationApprovalFormView builds the view of one approval step. payload is
// the recorded callback payload, empty while the approval waits.
func operationApprovalFormView(approval api.OperationApprovalSpec, form api.OperationApprovalForm, payload json.RawMessage) (api.OperationApprovalFormView, error) {
	view := api.OperationApprovalFormView{Step: approval.Step, Approver: approval.Approver, InputSchema: form.InputSchema, Required: form.Required}
	if len(payload) == 0 {
		return view, nil
	}
	var recorded api.OperationApprovalInputPayload
	if err := json.Unmarshal(payload, &recorded); err != nil {
		return view, fmt.Errorf("state: decode approval decision: %w", err)
	}
	view.Decision = &recorded.Approval
	return view, nil
}

// approvalForCaller returns the definition's approval step a caller may read:
// a customer (tenantID set) reads only the steps it approves.
func approvalForCaller(def OperationDefinition, step, tenantID string) (api.OperationApprovalSpec, error) {
	approval, ok := operations.ApprovalFor(def.Spec, step)
	if !ok || (tenantID != "" && approval.Approver != api.OperationApproverCustomer) {
		return api.OperationApprovalSpec{}, ErrNotFound
	}
	return approval, nil
}
