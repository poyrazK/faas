package managedpostgres

import "context"

// UpgradeMajorRequest upgrades a database in place to a later PostgreSQL
// major (ADR-1027).
type UpgradeMajorRequest struct {
	AccountID, DatabaseID, RequestID string
	PostgresMajor                    int
}

// UpgradeMajor commits customer intent only, as Resize does: the shared
// compute-change journal serializes it with resizes and lifecycle
// mutations, and the reconciler drives the provider. Only the major
// changes. An upgrade the provider rejects for good ends failed with a
// stable ErrorCode, the database still on its old major.
func (s *Service) UpgradeMajor(ctx context.Context, request UpgradeMajorRequest) (ResizeOperation, error) {
	if request.PostgresMajor <= 0 {
		return ResizeOperation{}, ErrInvalid
	}
	return s.reserveComputeChange(ctx, ResizeDatabaseRequest{AccountID: request.AccountID, DatabaseID: request.DatabaseID,
		RequestID: request.RequestID, TargetPostgresMajor: request.PostgresMajor}, nil)
}

// GetMajorUpgrade returns one of the database's major upgrades.
func (s *Service) GetMajorUpgrade(ctx context.Context, account, database, id string) (ResizeOperation, error) {
	store, ok := s.store.(ResizeStore)
	if !ok {
		return ResizeOperation{}, ErrUnsupported
	}
	if !validResizeRequestID(id) {
		return ResizeOperation{}, ErrInvalid
	}
	operation, err := store.GetResize(ctx, account, id)
	if err == nil && (operation.DatabaseID != database || !operation.MajorUpgrade()) {
		return ResizeOperation{}, ErrNotFound
	}
	return operation, err
}
