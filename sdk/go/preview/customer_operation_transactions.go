package faas

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// CustomerOperationRequest is an explicitly negotiated HTTP receipt context.
// The current claim proof is validated and discarded; receipts grant no runtime
// authority. Obtain it only behind Gregale ingress, never from a public server.
type CustomerOperationRequest struct{ request OperationRequest }

func CustomerOperationRequestFromHTTP(r *http.Request, body []byte) (CustomerOperationRequest, error) {
	if r == nil || r.URL == nil {
		return CustomerOperationRequest{}, ErrInvalidOperationRequest
	}
	allowed := map[string]bool{
		"x-gregale-customer-operation-id": true, "x-gregale-customer-operation-receipt-version": true,
		"x-gregale-customer-operation-receipt-binding": true, "x-gregale-operation-attempt": true, "x-gregale-operation-capability": true,
	}
	for key := range r.Header {
		lower := strings.ToLower(key)
		if (strings.HasPrefix(lower, "x-gregale-operation-") || strings.HasPrefix(lower, "x-gregale-customer-operation-")) && !allowed[lower] {
			return CustomerOperationRequest{}, ErrInvalidOperationRequest
		}
	}
	values := make([]string, 9)
	for i, name := range []string{"X-Gregale-Customer-Operation-Receipt-Version", "X-Gregale-Customer-Operation-Receipt-Binding", "X-Gregale-Customer-Operation-Id", "X-Faas-Tenant-Id", "X-Faas-App-Id", "X-Faas-Platform-Tenant-Id", "X-Gregale-Operation-Attempt", "X-Gregale-Operation-Capability", "X-Faas-Invocation-Id"} {
		var list []string
		for key, items := range r.Header {
			if strings.EqualFold(key, name) {
				list = append(list, items...)
			}
		}
		if len(list) != 1 || list[0] == "" {
			return CustomerOperationRequest{}, ErrInvalidOperationRequest
		}
		values[i] = list[0]
	}
	attempt, err := strconv.ParseInt(values[6], 10, 32)
	if err != nil || attempt <= 0 || strconv.FormatInt(attempt, 10) != values[6] || values[0] != "1" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(values[1]) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(values[7]) || !operationUUID(values[8]) {
		return CustomerOperationRequest{}, ErrInvalidOperationRequest
	}
	request, err := normalizeOperationRequest(OperationRequest{managed: true, receiptBinding: values[1], OperationID: values[2], AccountID: values[3], AppID: values[4], PlatformTenantID: values[5], Generation: attempt, Method: r.Method, Path: r.URL.RequestURI(), Body: body})
	return CustomerOperationRequest{request: request}, err
}

func CustomerOperationRequestDigest(input CustomerOperationRequest) ([]byte, error) {
	if input.request.receiptBinding == "" {
		return nil, ErrInvalidOperationRequest
	}
	return OperationRequestDigest(input.request)
}

// WithCustomerOperationTransaction commits database writes and the ordinary
// JSON result together. An approved recovery replays the result before calling
// handler. Use only the supplied transaction; external effects are unsupported.
// Return Body unchanged as application/json. Install and retain
// CustomerOperationReceiptSchema as the application database owner.
func WithCustomerOperationTransaction(ctx context.Context, db *sql.DB, input CustomerOperationRequest, handler func(OperationSQLTransaction) (json.RawMessage, error)) (OperationTransactionResult, error) {
	if input.request.receiptBinding == "" || handler == nil {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	result, err := withOperationTransaction(ctx, db, input.request, func(tx OperationSQLTransaction) (OperationOutcome, error) {
		body, err := handler(tx)
		return OperationOutcome{Result: body}, err
	})
	if err != nil {
		return OperationTransactionResult{}, err
	}
	result.Body, err = customerOperationResult(result.Body)
	return result, err
}

func customerOperationResult(body []byte) (json.RawMessage, error) {
	var envelope ManagedOperationResult
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Result) == 0 || len(envelope.Effects) != 0 {
		return nil, ErrInvalidOperationRequest
	}
	return envelope.Result, nil
}
