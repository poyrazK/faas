//go:build !no_pg

// adr:375
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

type databaseWorkerFailureStore struct {
	*roleWorkerFailureStore
	loseDatabaseRecord   bool
	beforeDatabaseRead   func() error
	beforeDatabaseRecord func()
}

func (s *databaseWorkerFailureStore) RecordProjectEnvironmentClonePostgresDatabasePlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, parent string, sealed copydatabases.Sealed) (state.ProjectEnvironmentClonePostgresDatabasePlan, bool, error) {
	if s.beforeDatabaseRecord != nil {
		s.beforeDatabaseRecord()
	}
	r, first, err := s.PgStore.RecordProjectEnvironmentClonePostgresDatabasePlan(ctx, l, id, parent, sealed)
	if err == nil && s.loseDatabaseRecord {
		s.loseDatabaseRecord = false
		return state.ProjectEnvironmentClonePostgresDatabasePlan{}, false, managedpostgres.ErrUnavailable
	}
	return r, first, err
}
func (s *databaseWorkerFailureStore) ProjectEnvironmentClonePostgresDatabasePlanForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresDatabasePlan, error) {
	if s.beforeDatabaseRead != nil {
		if err := s.beforeDatabaseRead(); err != nil {
			return state.ProjectEnvironmentClonePostgresDatabasePlan{}, err
		}
	}
	return s.PgStore.ProjectEnvironmentClonePostgresDatabasePlanForLease(ctx, l, id)
}

type databaseWorkerFixture struct {
	x                    *roleWorkerFixture
	store                *databaseWorkerFailureStore
	exports, unprojected copyinventory.ExportPlan
	closed               string
	closedOID            uint32
}

func cloneDatabaseWorkerFixture(t *testing.T, configure ...func(*roleWorkerFixture)) *databaseWorkerFixture {
	t.Helper()
	f := &databaseWorkerFixture{closed: "grg_db_worker/é ' ; " + uuid.NewString()[:12]}
	x := cloneRoleWorkerFixture(t, func(x *roleWorkerFixture) {
		if _, err := x.f.pool.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{f.closed}.Sanitize()+" TEMPLATE template0 CONNECTION LIMIT 6 ALLOW_CONNECTIONS false"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := x.f.pool.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{f.closed}.Sanitize()); err != nil {
				t.Error(err)
			}
		})
		if _, err := x.f.pool.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{f.closed}.Sanitize()+" SET app.stage_secret TO 'original-stage-setting'"); err != nil {
			t.Fatal(err)
		}
		for _, prepare := range configure {
			prepare(x)
		}
	})
	f.x = x
	f.store = &databaseWorkerFailureStore{roleWorkerFailureStore: x.store}
	x.f.srv.store = f.store
	owned, err := x.store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	i, err := copyinventory.OpenInventory(mfaIdentities(), owned.Sealed.Scope, owned.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	c, err := i.DatabaseCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	var ownerOID uint32
	for _, d := range c.Databases {
		if d.Name == f.closed {
			f.closedOID, ownerOID = d.OID, d.OwnerOID
		}
	}
	f.unprojected, err = i.PlanExports(owned.Sealed.Scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.exports, err = i.PlanExports(owned.Sealed.Scope, []copyinventory.OriginalAdmission{{DatabaseOID: f.closedOID, OwnerOID: ownerOID, DatabaseName: f.closed, OriginalAllowConnections: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Every new target database is known from the original frozen catalogue.
		// Provider/bootstrap/system baseline entries must survive this fixture.
		reqs, _ := f.exports.RequirementsForWorker()
		for _, d := range reqs {
			if d.Database.Name == "postgres" || d.Database.Name == "template0" || d.Database.Name == "template1" || d.Database.Name == x.target.DatabaseName {
				continue
			}
			if _, err := x.targetRoot.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{d.Database.Name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}
func (f *databaseWorkerFixture) exists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	if err := f.x.targetRoot.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
func (f *databaseWorkerFixture) prepare(t *testing.T) []copydatabases.Receipt {
	t.Helper()
	r, err := f.x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), f.x.f.lease, f.x.source, f.exports)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func workerDatabasePins(t *testing.T, receipts []copydatabases.Receipt) map[string]copyarchive.RestoreTarget {
	t.Helper()
	pins := map[string]copyarchive.RestoreTarget{}
	for _, r := range receipts {
		p, err := r.TargetForWorker()
		if err != nil || r.CreatedAt().IsZero() {
			t.Fatal("incomplete database receipt", err)
		}
		pins[p.DatabaseName] = p
	}
	return pins
}

func TestPGClonePostgresDatabaseWorkerRetainsCompleteOriginalPlanBeforeCreateAndRecoversAcrossHandoff(t *testing.T) {
	f := cloneDatabaseWorkerFixture(t)
	x := f.x
	f.store.loseDatabaseRecord = true
	if r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), x.f.lease, x.source, f.exports); !errors.Is(err, managedpostgres.ErrUnavailable) || len(r) != 0 || f.exists(t, f.closed) || !x.roleExists(t) || x.p.targetSQLCalls != 3 {
		t.Fatalf("lost plan record authorized database DDL: %v calls=%d", err, x.p.targetSQLCalls)
	}
	original, err := f.store.ProjectEnvironmentClonePostgresDatabasePlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, oldKey} }
	setSecretRecipient = nil
	service := x.f.srv.managedPostgres
	x.f.srv.managedPostgres = nil
	if _, owner, target, err := x.f.srv.projectEnvironmentClonePostgresDatabasePlan(t.Context(), x.f.lease, x.source, f.exports); err != nil || !sameClonePostgresDatabasePlan(owner, original) || target != x.target || x.p.targetSQLCalls != 3 {
		t.Fatal("original database plan recovery required SQL/provider/current key", err)
	}
	x.f.srv.managedPostgres = service
	old := x.f.lease
	if err = f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), old, 0); err != nil {
		t.Fatal(err)
	}
	x.f.lease, err = f.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), old, x.source, f.exports); !errors.Is(err, state.ErrConflict) || len(r) != 0 || x.p.targetSQLCalls != 3 {
		t.Fatal("stale lease dispatched database DDL", err)
	}
	if _, err = x.f.pool.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{f.closed}.Sanitize()+" CONNECTION LIMIT 19"); err != nil {
		t.Fatal(err)
	}
	r := f.prepare(t)
	pins := workerDatabasePins(t, r)
	reqs, _ := f.exports.RequirementsForWorker()
	if len(pins) != len(reqs) {
		t.Fatal("worker omitted captured databases")
	}
	for _, d := range reqs {
		if _, ok := pins[d.Database.Name]; !ok {
			t.Fatal("missing database disposition")
		}
	}
	p := pins[f.closed]
	if p.DatabaseOID == f.closedOID || p.OwnerID != x.target.OwnerID || !p.Scope.Equal(x.target.Scope) {
		t.Fatal("created database lost independent identity or placement")
	}
	var exact bool
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT NOT datallowconn AND NOT datistemplate AND datconnlimit=6 AND datdba=$2::oid FROM pg_database WHERE oid=$1::oid", p.DatabaseOID, p.RoleOID).Scan(&exact); err != nil || !exact {
		t.Fatal("worker changed original closed/bootstrap preparation", err)
	}
	owner, err := f.store.ProjectEnvironmentClonePostgresDatabasePlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil || !sameClonePostgresDatabasePlan(owner, original) {
		t.Fatal("handoff rebased original encrypted plan", err)
	}
	again := f.prepare(t)
	for n := range r {
		if !r[n].CreatedAt().Equal(again[n].CreatedAt()) {
			t.Fatal("exact retry changed completion time")
		}
	}
	x.assertPrivate(t)
	before := x.p.targetSQLCalls
	if r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), x.f.lease, x.source, f.unprojected); !errors.Is(err, managedpostgres.ErrConflict) || len(r) != 0 || x.p.targetSQLCalls != before {
		t.Fatal("discarded original admission projection accepted", err)
	}
}

func TestPGClonePostgresDatabaseWorkerRecoversTargetCommitAndProviderReplyLoss(t *testing.T) {
	for _, phase := range []string{"created", "provider"} {
		t.Run(phase, func(t *testing.T) {
			f := cloneDatabaseWorkerFixture(t)
			x := f.x
			if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresDatabasePlan(t.Context(), x.f.lease, x.source, f.exports); err != nil {
				t.Fatal(err)
			}
			var failed bool
			if phase == "created" {
				f.store.beforeDatabaseRead = func() error {
					if !failed && f.exists(t, f.closed) {
						failed = true
						return managedpostgres.ErrUnavailable
					}
					return nil
				}
			} else {
				x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
			}
			r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), x.f.lease, x.source, f.exports)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || len(r) != 0 || !f.exists(t, f.closed) {
				t.Fatal("unknown target/provider reply returned complete set or dropped owned DB", err)
			}
			x.p.targetSQLAfterError = nil
			f.store.beforeDatabaseRead = nil
			var id uint32
			if err = x.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_database WHERE datname=$1", f.closed).Scan(&id); err != nil {
				t.Fatal(err)
			}
			recovered := f.prepare(t)
			if workerDatabasePins(t, recovered)[f.closed].DatabaseOID != id {
				t.Fatal("recovery replaced physical target DB")
			}
			x.assertPrivate(t)
		})
	}
}

func TestPGClonePostgresDatabaseWorkerRejectsDispatchDriftAndChangedOriginalInputs(t *testing.T) {
	for _, fault := range []string{"stale", "phase", "owner", "target baseline", "inventory parent", "role parent", "wrong capture"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneDatabaseWorkerFixture(t)
			x := f.x
			if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresDatabasePlan(t.Context(), x.f.lease, x.source, f.exports); err != nil {
				t.Fatal(err)
			}
			before := x.p.targetSQLCalls
			exports := f.exports
			switch fault {
			case "stale":
				if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), x.f.lease, 0); err != nil {
					t.Fatal(err)
				}
			case "phase":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE managed_postgres_databases SET backend_fingerprint=$1 WHERE id=$2", strings.Repeat("f", 64), x.target.OwnerID); err != nil {
					t.Fatal(err)
				}
			case "target baseline":
				if _, err := x.targetRoot.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{f.closed}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			case "inventory parent":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_database_plans SET inventory_ciphertext_sha256=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "role parent":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_database_plans SET role_plan_ciphertext_sha256=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "wrong capture":
				exports = copyinventory.ExportPlan{}
			}
			r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), x.f.lease, x.source, exports)
			if err == nil || len(r) != 0 {
				t.Fatal("unqualified dispatch returned preparation receipts")
			}
			if fault != "target baseline" && (x.p.targetSQLCalls != before || f.exists(t, f.closed)) {
				t.Fatal("invalid metadata dispatched target SQL")
			}
			x.assertPrivate(t)
		})
	}
}

func TestPGClonePostgresDatabaseWorkerRecipientAndProviderFailuresBeforeRetention(t *testing.T) {
	for _, fault := range []string{"recipient", "provider pre", "provider post", "role parent changed at record"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneDatabaseWorkerFixture(t)
			x := f.x
			if fault == "recipient" {
				setSecretRecipient = func() *age.X25519Recipient { key, _ := age.GenerateX25519Identity(); return key.Recipient() }
			}
			if fault == "provider pre" {
				x.beforeSQL = func(context.Context) error { return managedpostgres.ErrConflict }
			}
			if fault == "provider post" {
				x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
			}
			// Initial role ownership is still required; database failures cannot replace
			// or publish a data identity, even if role seeding already committed.
			if fault == "role parent changed at record" {
				f.store.beforeDatabaseRecord = func() {
					if x.p.targetSQLCalls >= 3 {
						if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_role_plans SET ciphertext_sha256=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			r, err := x.f.srv.projectEnvironmentClonePostgresDatabases(t.Context(), x.f.lease, x.source, f.exports)
			if err == nil || len(r) != 0 || f.exists(t, f.closed) {
				t.Fatal("failed capture retained successful preparation or created database", err)
			}
			var count int
			if err := x.f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_database_plans WHERE operation_id=$1", x.f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed provider/recipient check retained database plan", err)
			}
			x.assertPrivate(t)
		})
	}
}
