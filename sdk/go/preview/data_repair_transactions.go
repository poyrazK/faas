package faas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrDataRepairCaptureMissing     = errors.New("repair_capture_missing")
	ErrDataRepairCaptureMismatch    = errors.New("repair_capture_mismatch")
	ErrDataRepairCaptureIncomplete  = errors.New("repair_capture_incomplete")
	ErrDataRepairCaptureUnavailable = errors.New("repair_capture_unavailable: check the owner upgrade and writer grants")
)

const captureTransactionSQL = `SELECT gregale_repair.capture_transaction($1::uuid,$2::jsonb,$3,$4,$5,$6::bytea,$7::bytea,$8)`

func captureTransactionError(err error) error {
	if err == nil {
		return nil
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "GR001":
			return ErrDataRepairCaptureMissing
		case "GR002":
			return ErrDataRepairCaptureMismatch
		case "GR003", "22023":
			return ErrDataRepairCaptureIncomplete
		}
	}
	return ErrDataRepairCaptureUnavailable
}

// WithRepairableOperationTransaction owns capture, business writes and the
// operation receipt in one transaction. Replay requires the original capture
// configuration and its immutable receipt binding; it never reruns the handler.
func WithRepairableOperationTransaction(ctx context.Context, db *sql.DB, input OperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (OperationOutcome, error)) (OperationTransactionResult, error) {
	if input.receiptBinding != "" {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	return repairableOperationTransaction(ctx, db, input, config, handler)
}

// WithRepairableCustomerOperationTransaction is the result-only Customer
// Operations equivalent, using the existing negotiated receipt context.
func WithRepairableCustomerOperationTransaction(ctx context.Context, db *sql.DB, input CustomerOperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (json.RawMessage, error)) (OperationTransactionResult, error) {
	if input.request.receiptBinding == "" || handler == nil {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	result, err := repairableOperationTransaction(ctx, db, input.request, config, func(tx OperationSQLTransaction) (OperationOutcome, error) {
		body, err := handler(tx)
		return OperationOutcome{Result: body}, err
	})
	if err != nil {
		return OperationTransactionResult{}, err
	}
	result.Body, err = customerOperationResult(result.Body)
	return result, err
}

func repairableOperationTransaction(ctx context.Context, db *sql.DB, input OperationRequest, config DataRepairCapture, handler func(OperationSQLTransaction) (OperationOutcome, error)) (OperationTransactionResult, error) {
	request, err := normalizeOperationRequest(input)
	if err != nil || handler == nil || !validDataRepairConfig(config) || strings.TrimSpace(config.Table) != config.Table || !strings.Contains(config.Table, ".") {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	digest, err := OperationRequestDigest(request)
	if err != nil {
		return OperationTransactionResult{}, err
	}
	scope := map[string]string{"account_id": request.AccountID, "app_id": request.AppID, "environment": config.Environment}
	if request.PlatformTenantID != "" {
		scope["platform_tenant_id"] = request.PlatformTenantID
	}
	raw, _ := json.Marshal(scope)
	protocol := "managed"
	if request.receiptBinding != "" {
		protocol = "customer"
	}
	phase := func(ctx context.Context, tx OperationSQLTransaction, mode string, response []byte) error {
		_, err := tx.ExecContext(ctx, captureTransactionSQL, request.OperationID, string(raw), config.Release, config.Table, protocol, digest, response, mode)
		return captureTransactionError(err)
	}
	return guardedOperationTransaction(ctx, db, request, func(tx OperationSQLTransaction) (OperationOutcome, error) {
		if err := phase(ctx, tx, "begin", nil); err != nil {
			return OperationOutcome{}, err
		}
		outcome, err := handler(tx)
		if err != nil {
			return OperationOutcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `SELECT gregale_repair.finish_operation($1::uuid)`, request.OperationID); err != nil {
			return OperationOutcome{}, captureTransactionError(err)
		}
		return outcome, nil
	}, func(ctx context.Context, tx OperationSQLTransaction, body []byte, replayed bool) error {
		mode := "seal"
		if replayed {
			mode = "replay"
		}
		response := sha256.Sum256(body)
		return phase(ctx, tx, mode, response[:])
	})
}
