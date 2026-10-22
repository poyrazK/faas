// adr: 20261019001500
package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationApprovalFormFromPG(row sqlc.CustomerOperationApprovalForm) api.OperationApprovalForm {
	return api.OperationApprovalForm{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), Scope: row.Scope, Operation: row.Operation, Step: row.Step,
		InputSchema: append([]byte(nil), row.InputSchema...), Required: row.Required, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
}

// operationApprovalFormTx returns the step's form, or nil when it has none
// or the account is outside the rollout.
func operationApprovalFormTx(ctx context.Context, db sqlc.DBTX, accountID string, def OperationDefinition, step string) (*api.OperationApprovalForm, error) {
	if !api.OperationApprovalInputsEnabled(accountID) {
		return nil, nil
	}
	row, err := sqlc.New().GetOperationApprovalFormByKey(ctx, db, sqlc.GetOperationApprovalFormByKeyParams{AppID: mustPgUUID(def.AppID), Scope: def.Scope,
		Operation: def.Spec.Name, Step: step})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	form := operationApprovalFormFromPG(row)
	return &form, nil
}

func (s *PgStore) PutOperationApprovalForm(ctx context.Context, in OperationApprovalFormInput) (api.OperationApprovalForm, bool, error) {
	account, err := operationUUID(in.AccountID)
	if err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	acct, err := s.AccountByID(ctx, in.AccountID)
	if err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	limits := api.MustLimitsFor(acct.Plan).Operations
	scope, schema, err := validateOperationApprovalFormRequest(in.Request, limits)
	if err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	now := pgtype.Timestamptz{Time: in.Now.UTC().Truncate(time.Microsecond), Valid: true}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCustomerOperationAccount(ctx, tx, account); err != nil {
		return api.OperationApprovalForm{}, false, mapErr(err)
	}
	app := mustPgUUID(in.AppID)
	row, err := q.LockOperationApprovalFormByKey(ctx, tx, sqlc.LockOperationApprovalFormByKeyParams{AppID: app, Scope: scope, Operation: in.Request.Operation, Step: in.Request.Step})
	created := errors.Is(err, pgx.ErrNoRows)
	switch {
	case err == nil:
		row, err = q.UpdateOperationApprovalForm(ctx, tx, sqlc.UpdateOperationApprovalFormParams{ID: row.ID, InputSchema: schema, Required: in.Request.Required, Now: now})
	case created:
		var count int64
		if count, err = q.CountOperationApprovalForms(ctx, tx, app); err == nil && count >= int64(limits.ApprovalFormsPerApp) {
			return api.OperationApprovalForm{}, false, NewOperationLimitError("approval_forms_per_app", int64(limits.ApprovalFormsPerApp), count+1)
		}
		if err == nil {
			row, err = q.InsertOperationApprovalForm(ctx, tx, sqlc.InsertOperationApprovalFormParams{ID: mustPgUUID(uuid.NewString()), AccountID: account, AppID: app,
				Scope: scope, Operation: in.Request.Operation, Step: in.Request.Step, InputSchema: schema, Required: in.Request.Required, Now: now})
		}
	}
	if err != nil {
		return api.OperationApprovalForm{}, false, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.OperationApprovalForm{}, false, err
	}
	return operationApprovalFormFromPG(row), created, nil
}

func (s *PgStore) OperationApprovalForms(ctx context.Context, accountID, appID string) ([]api.OperationApprovalForm, error) {
	rows, err := sqlc.New().ListOperationApprovalForms(ctx, s.pool, sqlc.ListOperationApprovalFormsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, err
	}
	out := make([]api.OperationApprovalForm, 0, len(rows))
	for _, row := range rows {
		out = append(out, operationApprovalFormFromPG(row))
	}
	return out, nil
}

func (s *PgStore) DeleteOperationApprovalForm(ctx context.Context, accountID, appID, id string) error {
	form, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().DeleteOperationApprovalForm(ctx, s.pool, sqlc.DeleteOperationApprovalFormParams{ID: mustPgUUID(form.String()),
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) OperationApprovalFormView(ctx context.Context, accountID, tenantID, operationID, step string) (api.OperationApprovalFormView, error) {
	op, err := s.OperationByID(ctx, accountID, tenantID, operationID)
	if err != nil {
		return api.OperationApprovalFormView{}, err
	}
	def, err := s.OperationDefinitionByID(ctx, op.AccountID, op.DefinitionID)
	if err != nil {
		return api.OperationApprovalFormView{}, err
	}
	approval, err := approvalForCaller(def, step, tenantID)
	if err != nil || op.WorkflowRunID == "" {
		return api.OperationApprovalFormView{}, ErrNotFound
	}
	form, err := operationApprovalFormTx(ctx, s.pool, op.AccountID, def, step)
	if err != nil {
		return api.OperationApprovalFormView{}, err
	}
	if form == nil {
		return api.OperationApprovalFormView{}, ErrNotFound
	}
	payload, err := sqlc.New().GetWorkflowCallbackPayload(ctx, s.pool, mustPgUUID(api.WorkflowCallbackID(op.WorkflowRunID, step)))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.OperationApprovalFormView{}, err
	}
	return operationApprovalFormView(approval, *form, payload)
}
