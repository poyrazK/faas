// Package operation is apid's customer operations surface: operation
// definitions and admission, submissions and their history, delivery and
// retry, milestones and workflow state, artifacts and uploads, recovery,
// the doctor, and the live operation stream. It was lifted out of cmd/apid by
// ADR-977 (the package is singular to stay distinct from pkg/operations,
// which it builds on).
//
// Everything it needs from apid goes through Host, which cmd/apid implements
// by reading its own fields at call time.
package operation

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Host is what the operations surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	// Subscribe listens on pg_notify channels for the operation stream.
	Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
	// ObjectStorage is nil when no object storage backend is configured.
	ObjectStorage() *objectstorage.Registry
	ObjectStorageEnabled() bool
	AdmitObjectURL(ctx context.Context, bucket state.ObjectBucket, req objectstorage.SignRequest) error
	// Now is apid's clock, which tests can pin.
	Now() time.Time
}

// Service is the operations surface. apid configures Preview and
// WorkloadVerifier at boot and ArtifactStorage via WithOperationArtifactStorage.
type Service struct {
	ProductControlEnabled bool
	Host                  Host

	// AdmissionEnabled is the private fixture fallback; startup always
	// installs the scoped Preview gate.
	AdmissionEnabled bool
	Preview          *operations.PreviewAdmission
	WorkloadVerifier *workloadidentity.Verifier
	ArtifactStorage  storage.StorageBackend
	Metrics          *Metrics // ADR-20261016123221; nil counts nothing

	// SchedulesReleased and ChildrenReleased are the operation_schedules
	// and operation_children platform flags (ADR 20261021080640), wired by
	// apid. Nil falls back to the environment allowlists.
	SchedulesReleased func(accountID string) bool
	ChildrenReleased  func(accountID string) bool

	artifactBudget operationArtifactBudget
	streamHub      *operationStreamHub
}

// ArtifactBudgetInUse reports the artifact transfers and spool bytes this
// node currently holds, so tests can assert reservations are released.
func (s *Service) ArtifactBudgetInUse() (transfers int, bytes int64) {
	s.artifactBudget.mu.Lock()
	defer s.artifactBudget.mu.Unlock()
	return s.artifactBudget.transfers, s.artifactBudget.bytes
}

// schedulesOpen reports whether Operation schedules are released to the
// account.
func (s *Service) schedulesOpen(accountID string) bool {
	if s.SchedulesReleased != nil {
		return s.SchedulesReleased(accountID)
	}
	return api.OperationSchedulesEnabled(accountID)
}

// childrenOpen reports whether child Operations are released to the account.
func (s *Service) childrenOpen(accountID string) bool {
	if s.ChildrenReleased != nil {
		return s.ChildrenReleased(accountID)
	}
	return api.OperationChildrenEnabled(accountID)
}
