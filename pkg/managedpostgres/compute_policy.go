package managedpostgres

import "context"

type ChangeComputePolicyRequest struct {
	AccountID, DatabaseID, RequestID string
	ScaleToZero                      bool
}

// ChangeComputePolicy preserves the service class and every other specification
// field. The shared journal serializes it with resizing and lifecycle mutations.
func (s *Service) ChangeComputePolicy(ctx context.Context, request ChangeComputePolicyRequest) (ResizeOperation, error) {
	return s.reserveComputeChange(ctx, ResizeDatabaseRequest{AccountID: request.AccountID, DatabaseID: request.DatabaseID,
		RequestID: request.RequestID}, &request.ScaleToZero)
}

func (s *Service) GetComputePolicyChange(ctx context.Context, account, database, id string) (ResizeOperation, error) {
	store, ok := s.store.(ResizeStore)
	if !ok {
		return ResizeOperation{}, ErrUnsupported
	}
	if !validResizeRequestID(id) {
		return ResizeOperation{}, ErrInvalid
	}
	operation, err := store.GetResize(ctx, account, id)
	if err == nil && (operation.DatabaseID != database || !operation.PolicyChange) {
		return ResizeOperation{}, ErrNotFound
	}
	return operation, err
}
