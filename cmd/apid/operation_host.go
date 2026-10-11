package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// WithOperationArtifactStorage installs the backend for operation artifacts
// and result blobs.
func (s *server) WithOperationArtifactStorage(backend storage.StorageBackend) *server {
	s.operation.ArtifactStorage = backend
	return s
}

// operationHost adapts *server to operation.Host (ADR-977). It reads server
// fields at call time, so tests that swap s.store or pin timeNow behave
// exactly as they did before the lift.
type operationHost struct{ s *server }

var _ operation.Host = operationHost{}

func (h operationHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h operationHost) Store() state.Store { return h.s.store }

func (h operationHost) Logger() *slog.Logger { return h.s.log }

func (h operationHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h operationHost) Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error) {
	return h.s.notif.Subscribe(ctx, channels)
}

func (h operationHost) ObjectStorage() *objectstorage.Registry { return h.s.objectStorage }

func (h operationHost) ObjectStorageEnabled() bool { return h.s.object.ObjectStorageEnabled() }

func (h operationHost) AdmitObjectURL(ctx context.Context, bucket state.ObjectBucket, req objectstorage.SignRequest) error {
	return h.s.object.AdmitObjectURL(ctx, bucket, req)
}

func (h operationHost) Now() time.Time { return timeNow() }
