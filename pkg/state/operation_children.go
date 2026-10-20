// adr: 20261018183000
package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrOperationChildDepth means a child Operation tried to start children.
var ErrOperationChildDepth = errors.New("state: a child operation cannot start children")

// OperationChild is one child Operation with its parent's key for it.
type OperationChild struct {
	Key       string
	Operation Operation
}

// OperationChildStart starts one child of the Operation the authority's
// current attempt runs.
type OperationChildStart struct {
	ParentID  string
	Authority OperationExecutionAuthority
	Request   api.OperationChildRequest
}

// OperationChildStore owns customer_operation_children.
type OperationChildStore interface {
	// StartChildOperation admits the child, or returns the child the key
	// already names with created false.
	StartChildOperation(ctx context.Context, in OperationChildStart) (OperationChild, bool, error)
	// OperationChildren lists a parent's children in start order. tenantID
	// scopes the parent to one customer; empty is the account's view.
	OperationChildren(ctx context.Context, accountID, tenantID, parentID string) ([]OperationChild, error)
}

func validateOperationChildRequest(req api.OperationChildRequest) error {
	if req.Operation == "" || len(req.Operation) > api.OperationNameMaxBytes {
		return fmt.Errorf("%w: operation names a definition of the parent's deployment", ErrInvalidArgument)
	}
	if req.Key == "" || len(req.Key) > api.OperationChildKeyMaxBytes || strings.ContainsAny(req.Key, "\x00\r\n") {
		return fmt.Errorf("%w: key is 1 to %d bytes without control line breaks", ErrInvalidArgument, api.OperationChildKeyMaxBytes)
	}
	return nil
}

// operationChildIdempotencyKey scopes the parent's key to the parent, so a
// retried parent attempt replays its child.
func operationChildIdempotencyKey(parentID, key string) string {
	return "child:" + parentID + ":" + key
}

// checkOperationChildParent refuses parents that may not start children.
// The caller has validated the execution authority.
func checkOperationChildParent(parent Operation, isChild bool) error {
	switch {
	case isChild:
		return ErrOperationChildDepth
	case parent.State.Terminal() || parent.CancellationRequested:
		return ErrOperationNotActive
	case parent.WorkflowRunID != "" || parent.JobRunID != "":
		return fmt.Errorf("%w: only HTTP Operations start children", ErrInvalidArgument)
	}
	return nil
}

func operationChildAdmission(parent Operation, childDefinitionID string, req api.OperationChildRequest) OperationAdmission {
	return OperationAdmission{AccountID: parent.AccountID, DefinitionID: childDefinitionID, PlatformTenantID: parent.PlatformTenantID,
		IdempotencyKey: operationChildIdempotencyKey(parent.ID, req.Key), Input: canonicalOperationScheduleInput(req.Input)}
}
