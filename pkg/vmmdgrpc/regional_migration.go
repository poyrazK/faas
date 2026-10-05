package vmmdgrpc

// adr: 628
import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

type regionalMigrationSourceReader interface {
	RegionalMigrationSource(context.Context, string, string) (runtimequotahost.MigrationRecord, error)
}
type regionalMigrationResumer interface {
	ResumeRegionalMigration(context.Context, string, string, runtimequota.Placement) error
}

type regionalMigrationPreflighter interface {
	PreflightRegionalMigration(context.Context, string, runtimequota.Placement) error
}

func (s *Server) validateRegionalMigrationPrepare(ctx context.Context, instance string, p runtimequota.Placement) error {
	if p.Seat == nil || !runtimequota.MatchesID(p.Permit.InstanceID, instance) {
		return runtimequota.ErrBinding
	}
	if _, ok := s.vmm.(regionalMigrationSourceReader); !ok {
		return fmt.Errorf("regional migration source retention unavailable: %w", runtimequota.ErrBinding)
	}
	if _, ok := s.vmm.(regionalMigrationResumer); !ok {
		return fmt.Errorf("regional migration rollback unavailable: %w", runtimequota.ErrBinding)
	}
	preflight, ok := s.vmm.(regionalMigrationPreflighter)
	if !ok {
		return fmt.Errorf("regional migration capture preflight unavailable: %w", runtimequota.ErrBinding)
	}
	return preflight.PreflightRegionalMigration(ctx, instance, p.Clone())
}

// Never interpret an unavailable journal or an unresolved source as a legacy
// lease. Expiry/ack retirement needs a future native source-bound verifier.
func (s *Server) requireIndependentMigration(ctx context.Context, instance, token string) error {
	reader, ok := s.vmm.(regionalMigrationSourceReader)
	if !ok {
		return nil
	}
	_, err := reader.RegionalMigrationSource(ctx, instance, token)
	if errors.Is(err, runtimequotahost.ErrMigrationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("regional migration requires source-bound reconciliation: %w", runtimequotahost.ErrBinding)
}

// Expiry may retry object/lease cleanup after a known completed rollback. It
// does no VM work and never interprets expiry as physical or serving release.
func (s *Server) cleanupCompletedRegionalMigration(ctx context.Context, m *activeMigration) (bool, error) {
	reader, ok := s.vmm.(regionalMigrationSourceReader)
	if !ok {
		return false, nil
	}
	r, err := reader.RegionalMigrationSource(ctx, m.instanceID, m.leaseToken)
	if errors.Is(err, runtimequotahost.ErrMigrationNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if !r.Completed {
		return true, runtimequotahost.ErrBinding
	}
	m.memKey, m.vmstateKey = r.MemStorageKey, r.VMStateStorageKey
	return true, nil
}

func (s *Server) cancelRegionalMigration(ctx context.Context, req *vmmdpb.CancelLiveMigrationRequest) (*vmmdpb.CancelLiveMigrationResponse, error) {
	p, err := regionalRuntimeFromProto(req.GetRegionalRuntime())
	if err != nil {
		return nil, err
	}
	if p == nil || p.Seat == nil || !runtimequota.MatchesID(p.Permit.InstanceID, req.GetInstanceId()) {
		return nil, runtimequota.ErrBinding
	}
	resumer, ok := s.vmm.(regionalMigrationResumer)
	if !ok {
		return nil, fmt.Errorf("regional migration rollback unavailable: %w", runtimequota.ErrBinding)
	}
	reader, ok := s.vmm.(regionalMigrationSourceReader)
	if !ok {
		return nil, runtimequotahost.ErrBinding
	}
	r, err := reader.RegionalMigrationSource(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if err != nil {
		return nil, err
	}
	if r.Source.Permit != p.Permit || r.Source.Seat == nil || r.Source.Seat.ID == p.Seat.ID {
		return nil, runtimequotahost.ErrBinding
	}
	// The host retains completion independently of the legacy lease. This
	// permits exact replay after successful cleanup deleted that lease.
	if err := resumer.ResumeRegionalMigration(ctx, req.GetInstanceId(), req.GetLeaseToken(), p.Clone()); err != nil {
		return nil, err
	}
	completed, err := reader.RegionalMigrationSource(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if err != nil {
		return nil, err
	}
	if !migrationCompletionMatches(completed, req.GetInstanceId(), req.GetLeaseToken(), *p) {
		return nil, runtimequotahost.ErrBinding
	}
	if err := s.cleanupRegionalMigrationLease(ctx, req, r); err != nil {
		return nil, err
	}
	return &vmmdpb.CancelLiveMigrationResponse{InstanceId: req.GetInstanceId(), LeaseToken: req.GetLeaseToken(), RegionalRuntime: append([]byte(nil), req.GetRegionalRuntime()...)}, nil
}

func migrationCompletionMatches(r runtimequotahost.MigrationRecord, instance, token string, p runtimequota.Placement) bool {
	return r.Version == 1 && r.Completed && r.InstanceKey == instance && r.LeaseToken == token &&
		r.Resume != nil && r.Resume.Permit == p.Permit && r.Resume.Seat != nil && p.Seat != nil && *r.Resume.Seat == *p.Seat
}

func (s *Server) cleanupRegionalMigrationLease(ctx context.Context, req *vmmdpb.CancelLiveMigrationRequest, r runtimequotahost.MigrationRecord) error {
	_, err := s.migrationLeaseForPhase(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if err != nil && !isMissingMigrationLease(err) {
		return err
	}
	m := &activeMigration{instanceID: req.GetInstanceId(), leaseToken: req.GetLeaseToken(), memKey: r.MemStorageKey, vmstateKey: r.VMStateStorageKey}
	if err := s.deleteMigrationSnapshot(ctx, m); err != nil {
		return err
	}
	if err := s.deleteDurableMigrationLease(ctx, req.GetLeaseToken()); err != nil {
		return err
	}
	if s.migrations != nil {
		s.migrations.deleteByLeaseToken(req.GetLeaseToken())
	}
	return nil
}

func isMissingMigrationLease(err error) bool {
	var missing errNoLease
	return errors.As(err, &missing)
}
