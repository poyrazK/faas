package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// DataRepairCapture opts one transaction into the owner-installed repair schema.
// Table and environment/release come from trusted application configuration.
type DataRepairCapture struct {
	Table       string
	Environment string
	Release     string
}

// WithDataRepairCapture belongs inside WithOperationTransaction's callback.
// It owns no transaction and performs no DDL. Propagate every returned error.
func WithDataRepairCapture[T any](ctx context.Context, tx OperationSQLTransaction, request OperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (T, error)) (T, error) {
	return withDataRepairCapture(ctx, tx, request, config, handler)
}

// WithCustomerDataRepairCapture is the Customer Operations receipt equivalent.
func WithCustomerDataRepairCapture[T any](ctx context.Context, tx OperationSQLTransaction, request CustomerOperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (T, error)) (T, error) {
	return withDataRepairCapture(ctx, tx, request.request, config, handler)
}

func withDataRepairCapture[T any](ctx context.Context, tx OperationSQLTransaction, input OperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (T, error)) (T, error) {
	var zero T
	request, err := normalizeOperationRequest(input)
	if err != nil {
		return zero, err
	}
	if tx == nil || handler == nil || !validDataRepairConfig(config) {
		return zero, ErrInvalidOperationRequest
	}
	scope := map[string]string{"account_id": request.AccountID, "app_id": request.AppID, "environment": config.Environment}
	if request.PlatformTenantID != "" {
		scope["platform_tenant_id"] = request.PlatformTenantID
	}
	raw, _ := json.Marshal(scope)
	if _, err = tx.ExecContext(ctx, `SELECT gregale_repair.begin_operation($1::uuid,$2::jsonb,$3,$4::regclass)`, request.OperationID, string(raw), config.Release, config.Table); err != nil {
		return zero, fmt.Errorf("begin data repair capture: %w", err)
	}
	result, err := handler(tx)
	if err != nil {
		return zero, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT gregale_repair.finish_operation($1::uuid)`, request.OperationID); err != nil {
		return zero, fmt.Errorf("finish data repair capture: %w", err)
	}
	return result, nil
}

func validDataRepairConfig(config DataRepairCapture) bool {
	for _, field := range []struct {
		value string
		limit int
	}{{config.Table, dataRepairTableMaxBytes}, {config.Environment, dataRepairContextMaxBytes}, {config.Release, dataRepairContextMaxBytes}} {
		if field.value == "" || len(field.value) > field.limit || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) {
			return false
		}
	}
	return true
}
