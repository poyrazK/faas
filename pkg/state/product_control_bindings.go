package state

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func productRequestFingerprint(req ProductFeatureRequest) (string, error) {
	if req.IdempotencyKey == "" {
		return "", nil // Binding inspection has no submission identity.
	}
	if len(req.IdempotencyKey) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.IdempotencyKey, "\r\n\x00") {
		return "", ErrInvalidArgument
	}
	if len(req.Input) > api.OperationSubmissionMaxBytes {
		return "", NewOperationLimitError("submission_bytes", api.OperationSubmissionMaxBytes, int64(len(req.Input)))
	}
	canonical, err := operations.CanonicalJSON(req.Input)
	if err != nil {
		return "", productCatalogError(err)
	}
	return operations.InputFingerprint(canonical)
}

func productRetryDefinition(op *Operation, expiry time.Time, fingerprint, expected string, now time.Time) (string, error) {
	if !expiry.After(now) && (op == nil || !operationIsActive(*op)) {
		return "", nil
	}
	if fingerprint != expected {
		return "", ErrOperationInputConflict
	}
	if op == nil || !operationRetained(*op, now) {
		return "", ErrOperationExpired
	}
	return op.DefinitionID, nil
}
