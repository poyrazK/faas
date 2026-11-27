package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

type migrationActivationProvider interface {
	ControlMigrationActivation(context.Context, string, dbguest.Op, dbmigration.Activation) error
}

type logicalMigrationSource interface {
	LogicalMigrationSource(context.Context, string) (Endpoint, string, error)
}

// SealMigrationTarget restricts a stopped subscriber to the cutover's exact
// staged credentials. The source endpoint comes from the source provider,
// rather than the client request or any staged secret.
func (s *Service) SealMigrationTarget(ctx context.Context, c Cutover, operation string) error {
	if c.State != CutoverPrepared && c.State != CutoverVerifying && c.State != CutoverVerified {
		return ErrConflict
	}
	source, err := s.registry.Resolve(c.Source.BackendID, c.Source.BackendFingerprint)
	if err != nil {
		return err
	}
	identity, ok := source.Provider.(logicalMigrationSource)
	if !ok {
		return ErrUnsupported
	}
	endpoint, database, err := identity.LogicalMigrationSource(ctx, c.Source.ProviderResourceID)
	if err != nil {
		return err
	}
	a := dbmigration.Activation{SwitchID: c.ID, OperationID: operation, SourceDatabase: database,
		SourcePin: dbmigration.EndpointPin(c.Target.ProviderResourceID, endpoint.Host, int(endpoint.Port))}
	for _, m := range c.Credentials {
		a.Roles = append(a.Roles, m.Sealed.ProviderIdentityID)
	}
	return s.controlMigrationActivation(ctx, c.Target, dbguest.OpMigrationSeal, a)
}

// OpenMigrationTarget is called only after the cutover transaction
// published its replacement bindings. Re-read that durable evidence
// before authorizing the guest to accept ordinary logins.
func (s *Service) OpenMigrationTarget(ctx context.Context, account, id, target, operation string) error {
	store, ok := s.store.(interface {
		GetCutover(context.Context, string, string) (Cutover, error)
	})
	if !ok {
		return ErrUnsupported
	}
	c, err := store.GetCutover(ctx, account, id)
	if err != nil {
		return err
	}
	if c.State != CutoverActivated || c.Target.ID != target {
		return ErrConflict
	}
	return s.controlMigrationActivation(ctx, c.Target, dbguest.OpMigrationOpen, dbmigration.Activation{SwitchID: id, OperationID: operation})
}

func (s *Service) AbortMigrationTarget(ctx context.Context, account, id, target, operation string) error {
	d, err := s.Get(ctx, account, target)
	if err != nil {
		return err
	}
	return s.controlMigrationActivation(ctx, d, dbguest.OpMigrationAbortActivation, dbmigration.Activation{SwitchID: id, OperationID: operation})
}

func (s *Service) controlMigrationActivation(ctx context.Context, d Database, op dbguest.Op, a dbmigration.Activation) error {
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return err
	}
	ctrl, ok := backend.Provider.(migrationActivationProvider)
	if !ok || backend.Driver != "firecracker" || d.Spec.Availability != AvailabilitySingleZone {
		return ErrUnsupported
	}
	return ctrl.ControlMigrationActivation(ctx, d.ProviderResourceID, op, a)
}

func (s *Service) ValidateMigrationActivationDatabases(source, target Database) error {
	if source.ID == target.ID || source.State != StateReady || target.State != StateReady ||
		target.Spec.Availability != AvailabilitySingleZone || !target.RestoreSlotUntil.IsZero() {
		return ErrConflict
	}
	from, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return err
	}
	to, err := s.registry.Resolve(target.BackendID, target.BackendFingerprint)
	if err != nil {
		return err
	}
	_, sourceSupported := from.Provider.(logicalMigrationSource)
	_, targetSupported := to.Provider.(migrationActivationProvider)
	if !sourceSupported || !targetSupported || to.Driver != "firecracker" {
		return ErrUnsupported
	}
	return nil
}
