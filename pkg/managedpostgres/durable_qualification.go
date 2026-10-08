package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DurableQualificationStage selects one disposable lost-acknowledgement probe.
// The factory must inject the fault only after the named effect succeeded.
type DurableQualificationStage struct {
	Access  CredentialAccess
	Fault   string
	Cleanup bool
}

const (
	QualificationProvisionAck  = "provision_ack"
	QualificationCredentialAck = "credential_ack"
	QualificationSecretAck     = "secret_ack"
)

type DurableLifecycleOptions struct {
	LifecycleQualificationOptions
	RunID                   string
	MigrationEnvironmentKey string
}

func (o DurableLifecycleOptions) DatabaseID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(o.RunID+"/"+o.AccountID+"/"+o.AppID+"/"+o.DatabaseName)).String()
}

func (o DurableLifecycleOptions) BindingID(access CredentialAccess) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(o.DatabaseID()+"/"+string(access))).String()
}

// Each session owns a fresh pool, registry and services for the same isolated
// catalog. Callbacks verify the real encrypted app-secret and a SQL workload;
// neither credentials nor connection strings belong in the returned report.
type DurableLifecycleSession struct {
	Catalog          *PostgresStore
	Service          *Service
	Bindings         *BindingService
	FaultObserved    func() bool
	VerifyCredential func(context.Context, Binding) error
	VerifyDeleted    func(context.Context, Binding) error
	VerifyWorkload   func(context.Context, Binding, Binding, bool) error
	Close            func()
}

type DurableLifecycleFactory func(context.Context, DurableQualificationStage) (DurableLifecycleSession, error)

// QualifyDurableLifecycle exercises lost acknowledgements with fresh SQL pools
// and reconstructed services. This is control-plane/SQL acceptance, not proof
// of a deployed guest, network egress, or scheduler credential retirement.
func QualifyDurableLifecycle(parent context.Context, factory DurableLifecycleFactory, options DurableLifecycleOptions) (report LifecycleQualificationReport, resultErr error) {
	report.Mode = DurableLifecycleMode
	record := func(name string, err error) bool { return recordQualificationCheck(&report, name, err, &resultErr) }
	if parent == nil || factory == nil || !canonicalQualificationUUID(options.RunID) || !canonicalQualificationUUID(options.AccountID) || !canonicalQualificationUUID(options.AppID) ||
		!ValidName(options.DatabaseName) || !validBindingScope(options.Scope) || !validEnvironmentKey(options.EnvironmentKey) ||
		!validEnvironmentKey(options.MigrationEnvironmentKey) || options.EnvironmentKey == options.MigrationEnvironmentKey || options.Access != CredentialReadWrite || options.Spec.Validate() != nil {
		record("lifecycle_identity", ErrInvalid)
		return
	}
	if options.Timeout <= 0 {
		options.Timeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	var session DurableLifecycleSession
	var previous *pgxpool.Pool
	var owned bool
	var bindingsReady bool
	open := func(stage DurableQualificationStage, openCtx context.Context) error {
		if session.Close != nil {
			session.Close()
			session.Close = nil
		}
		next, err := factory(openCtx, stage)
		if err != nil {
			return err
		}
		if next.Catalog == nil || next.Service == nil || next.Bindings == nil || next.Close == nil || next.VerifyCredential == nil || next.VerifyDeleted == nil || next.VerifyWorkload == nil || next.FaultObserved == nil {
			if next.Close != nil {
				next.Close()
			}
			return ErrInvalid
		}
		store, ok := next.Service.store.(*PostgresStore)
		dbStore, dbOK := next.Bindings.databases.(*PostgresStore)
		bindingStore, bindingOK := next.Bindings.bindings.(*PostgresStore)
		if !ok || !dbOK || !bindingOK || store != next.Catalog || dbStore != store || bindingStore != store || store.pool == previous || previous != nil && !sameQualificationCatalog(previous, store.pool) {
			next.Close()
			return ErrConflict
		}
		previous = store.pool
		session = next
		return nil
	}
	defer func() {
		if owned {
			cleanup, cleanupCancel := context.WithTimeout(context.WithoutCancel(parent), qualificationCleanupTimeout)
			defer cleanupCancel()
			if err := open(DurableQualificationStage{Access: CredentialReadWrite, Cleanup: true}, cleanup); err != nil {
				record("cleanup_restart_verified", err)
			} else if err := cleanupDurableQualification(cleanup, session, options); err != nil {
				record("cleanup_restart_verified", err)
			} else {
				record("binding_delete", nil)
				record("database_delete", nil)
				if err := open(DurableQualificationStage{Access: CredentialReadWrite, Cleanup: true}, cleanup); err != nil {
					record("cleanup_restart_verified", err)
				} else {
					record("cleanup_restart_verified", verifyDurableQualificationDeleted(cleanup, session, options, bindingsReady))
				}
			}
		}
		if session.Close != nil {
			session.Close()
		}
	}()
	if !record("catalog_postgres", open(DurableQualificationStage{Access: CredentialReadWrite, Fault: QualificationProvisionAck}, ctx)) {
		return
	}
	// Never adopt or compensate a name owned by another logical resource.
	rows, err := session.Catalog.List(ctx, options.AccountID)
	if !record("fixture_ownership", err) {
		return
	}
	for _, row := range rows {
		if row.Name == options.DatabaseName || row.ID == options.DatabaseID() {
			record("fixture_fresh", ErrConflict)
			return
		}
	}
	if _, err := session.Catalog.Get(ctx, options.AccountID, options.DatabaseID()); !errors.Is(err, ErrNotFound) {
		if err == nil {
			err = ErrConflict
		}
		record("fixture_fresh", err)
		return
	}
	record("fixture_fresh", nil)
	for _, check := range []string{"service_present", "binding_service_present", "lifecycle_identity", "lifecycle_access", "lifecycle_spec"} {
		record(check, nil)
	}
	request := CreateRequest{AccountID: options.AccountID, Name: options.DatabaseName, Spec: options.Spec}
	_, err = session.Service.Create(ctx, request)
	// The fault disconnects the catalog before the error reaches saga cleanup,
	// preserving the unfinished lease. Recover ownership through a fresh pool.
	// Cleanup independently verifies the reserved UUID before any compensation.
	owned = true
	faultErr := expectedQualificationFault(ctx, session, err)
	if !record("provision_ack_lost", faultErr) {
		return
	}
	if !record("restart_catalog_identity", open(DurableQualificationStage{Access: CredentialReadWrite}, ctx)) {
		return
	}
	row, lookupErr := session.Catalog.Get(ctx, options.AccountID, options.DatabaseID())
	if !record("database_create", durableQualificationOwnership(row, options, lookupErr)) {
		return
	}
	var database Database
	if !record("provision_lost_ack_recovered", waitDurableQualification(ctx, func() error {
		database, err = session.Service.Create(ctx, request)
		if err != nil {
			return err
		}
		if durableQualificationOwnership(database, options, nil) != nil {
			return ErrInvalid
		}
		if database.State != StateReady || database.ProviderResourceID == "" {
			return ErrUnavailable
		}
		return nil
	})) {
		return
	}
	record("database_ready", nil)
	providerID, dataID := database.ProviderResourceID, database.DataResourceID
	createBinding := func(access CredentialAccess) CreateBindingRequest {
		key := options.EnvironmentKey
		if access == CredentialMigration {
			key = options.MigrationEnvironmentKey
		}
		return CreateBindingRequest{AccountID: options.AccountID, DatabaseID: options.DatabaseID(), AppID: options.AppID, Scope: options.Scope, EnvironmentKey: key, Access: access}
	}
	var writer, migration Binding
	for _, probe := range []struct {
		access       CredentialAccess
		fault, check string
	}{
		{CredentialReadWrite, QualificationCredentialAck, "credentials_lost_ack_recovered"},
		{CredentialMigration, QualificationSecretAck, "secret_publication_lost_ack_recovered"},
	} {
		if !record(probe.check+"_setup", open(DurableQualificationStage{Access: probe.access, Fault: probe.fault}, ctx)) {
			return
		}
		_, err = session.Bindings.Create(ctx, createBinding(probe.access))
		if !record(probe.fault+"_lost", expectedQualificationFault(ctx, session, err)) {
			return
		}
		if !record(probe.check+"_restart", open(DurableQualificationStage{Access: probe.access}, ctx)) {
			return
		}
		var binding Binding
		if !record(probe.check, waitDurableQualification(ctx, func() error {
			binding, err = session.Bindings.Create(ctx, createBinding(probe.access))
			if err != nil {
				return err
			}
			if durableQualificationBindingOwnership(binding, options, probe.access) != nil {
				return ErrInvalid
			}
			if binding.State != BindingStateReady {
				return ErrUnavailable
			}
			return session.VerifyCredential(ctx, binding)
		})) {
			return
		}
		if probe.access == CredentialReadWrite {
			writer = binding
		} else {
			migration = binding
		}
	}
	record("binding_create", nil)
	record("binding_ready", nil)
	bindingsReady = true
	if !record("sealed_credentials_verified", session.VerifyCredential(ctx, writer)) || !record("workload_sql_round_trip", session.VerifyWorkload(ctx, writer, migration, true)) {
		return
	}
	if !record("rotation_setup", open(DurableQualificationStage{Access: CredentialReadWrite, Fault: QualificationSecretAck}, ctx)) {
		return
	}
	_, err = session.Bindings.Rotate(ctx, options.AccountID, options.BindingID(CredentialReadWrite))
	if !record("rotation_ack_lost", expectedQualificationFault(ctx, session, err)) {
		return
	}
	if !record("rotation_restart", open(DurableQualificationStage{Access: CredentialReadWrite}, ctx)) {
		return
	}
	oldGeneration, oldIdentity := writer.CredentialGeneration, writer.ProviderIdentityID
	if !record("rotation_lost_ack_recovered", waitDurableQualification(ctx, func() error {
		writer, err = session.Bindings.Rotate(ctx, options.AccountID, options.BindingID(CredentialReadWrite))
		if err != nil {
			return err
		}
		if durableQualificationBindingOwnership(writer, options, CredentialReadWrite) != nil || writer.CredentialGeneration != oldGeneration+1 || writer.ProviderIdentityID == oldIdentity {
			return ErrInvalid
		}
		if writer.State != BindingStateReady {
			return ErrUnavailable
		}
		return session.VerifyCredential(ctx, writer)
	})) {
		return
	}
	if !record("rotation_preserves_workload", session.VerifyWorkload(ctx, writer, migration, false)) {
		return
	}
	database, err = session.Catalog.Get(ctx, options.AccountID, options.DatabaseID())
	if err == nil && (database.ProviderResourceID != providerID || database.DataResourceID != dataID) {
		err = ErrConflict
	}
	record("dataset_identity_preserved", err)
	return
}

func canonicalQualificationUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func expectedQualificationFault(ctx context.Context, session DurableLifecycleSession, err error) error {
	if !session.FaultObserved() || !errors.Is(err, ErrUnavailable) || session.Catalog.pool.Ping(ctx) == nil {
		return ErrConflict
	}
	return nil
}

func sameQualificationCatalog(a, b *pgxpool.Pool) bool {
	x, y := a.Config().ConnConfig, b.Config().ConnConfig
	return x.Host == y.Host && x.Port == y.Port && x.Database == y.Database && x.User == y.User && x.RuntimeParams["search_path"] == y.RuntimeParams["search_path"]
}

func durableQualificationOwnership(row Database, o DurableLifecycleOptions, err error) error {
	if err != nil {
		return err
	}
	if row.ID != o.DatabaseID() || row.AccountID != o.AccountID || row.Name != o.DatabaseName || row.Spec != o.Spec {
		return ErrConflict
	}
	return nil
}

func waitDurableQualification(ctx context.Context, step func() error) error {
	for {
		err := step()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrConflict) {
			return err
		}
		if err := waitQualificationPoll(ctx, time.Second); err != nil {
			return err
		}
	}
}

func cleanupDurableQualification(ctx context.Context, s DurableLifecycleSession, o DurableLifecycleOptions) error {
	row, err := s.Catalog.Get(ctx, o.AccountID, o.DatabaseID())
	if err := durableQualificationOwnership(row, o, err); err != nil {
		return err
	}
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialMigration} {
		id := o.BindingID(access)
		binding, err := s.Catalog.GetBinding(ctx, o.AccountID, id)
		if errors.Is(err, ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if err := durableQualificationBindingOwnership(binding, o, access); err != nil {
			return err
		}
		if err := waitDurableQualification(ctx, func() error {
			b, err := s.Bindings.Delete(ctx, o.AccountID, id)
			if err != nil {
				return err
			}
			if b.State != BindingStateDeleted {
				return ErrUnavailable
			}
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = qualifyLifecycleDelete(ctx, s.Service, o.AccountID, o.DatabaseID(), Database{})
	return err
}

func durableQualificationBindingOwnership(b Binding, o DurableLifecycleOptions, access CredentialAccess) error {
	key := o.EnvironmentKey
	if access == CredentialMigration {
		key = o.MigrationEnvironmentKey
	}
	if b.ID != o.BindingID(access) || b.AccountID != o.AccountID || b.DatabaseID != o.DatabaseID() || b.AppID != o.AppID || b.Scope != o.Scope || b.EnvironmentKey != key || b.Access != access {
		return ErrConflict
	}
	return nil
}

func verifyDurableQualificationDeleted(ctx context.Context, s DurableLifecycleSession, o DurableLifecycleOptions, requireBindings bool) error {
	d, err := s.Catalog.Get(ctx, o.AccountID, o.DatabaseID())
	if err := durableQualificationOwnership(d, o, err); err != nil {
		return err
	}
	if d.State != StateDeleted {
		return ErrConflict
	}
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialMigration} {
		b, err := s.Catalog.GetBinding(ctx, o.AccountID, o.BindingID(access))
		if errors.Is(err, ErrNotFound) {
			if requireBindings {
				return ErrConflict
			}
			key := o.EnvironmentKey
			if access == CredentialMigration {
				key = o.MigrationEnvironmentKey
			}
			if err := s.VerifyDeleted(ctx, Binding{AccountID: o.AccountID, AppID: o.AppID, Scope: o.Scope, EnvironmentKey: key}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if durableQualificationBindingOwnership(b, o, access) != nil || b.State != BindingStateDeleted {
			return ErrConflict
		}
		if err := s.VerifyDeleted(ctx, b); err != nil {
			return err
		}
	}
	return nil
}
