//go:build !no_pg

// adr:375
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/state"
)

type roleWorkerFailureStore struct {
	*state.PgStore
	loseRecord bool
}

func (s *roleWorkerFailureStore) RecordProjectEnvironmentClonePostgresRolePlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, sealed copyroles.Sealed) (state.ProjectEnvironmentClonePostgresRolePlan, bool, error) {
	r, first, err := s.PgStore.RecordProjectEnvironmentClonePostgresRolePlan(ctx, l, id, sealed)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresRolePlan{}, false, managedpostgres.ErrUnavailable
	}
	return r, first, err
}

type roleWorkerFixture struct {
	f           cloneCoordinatorFixture
	store       *roleWorkerFailureStore
	p           *cloneSnapshotProvider
	source      capturedProjectEnvironmentDatabasePlan
	target      copyarchive.RestoreTarget
	targetRoot  *pgx.Conn
	member      string
	connections []*pgx.Conn
	beforeSQL   func(context.Context) error
}

func cloneRoleWorkerFixture(t *testing.T, configure ...func(*roleWorkerFixture)) *roleWorkerFixture {
	t.Helper()
	targetURL := os.Getenv("FAAS_COPY_ROLES_TARGET_DATABASE_URL")
	if targetURL == "" {
		t.Skip("independent local PostgreSQL target required for role worker contracts")
	}
	f, _, p, id := cloneReaderWorkerFixture(t, 16)
	x := &roleWorkerFixture{f: f, p: p, store: &roleWorkerFailureStore{PgStore: f.store.PgStore}, member: "grg_worker/é ; ' " + uuid.NewString()[:12]}
	var err error
	x.targetRoot, err = pgx.Connect(t.Context(), targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.targetRoot.Close(context.Background()) })
	if !strings.HasPrefix(x.targetRoot.Config().Host, "/") {
		t.Fatal("role worker contracts require private Unix sockets")
	}
	var sourceSystem, targetSystem string
	if err = f.pool.QueryRow(t.Context(), "SELECT system_identifier::text FROM pg_control_system()").Scan(&sourceSystem); err != nil {
		t.Fatal(err)
	}
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT system_identifier::text FROM pg_control_system()").Scan(&targetSystem); err != nil || sourceSystem == targetSystem {
		t.Fatalf("role worker target must be an independent cluster: %v", err)
	}
	if _, err = f.pool.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{x.member}.Sanitize()+" LOGIN NOINHERIT CONNECTION LIMIT 9 PASSWORD 'source-never-copied'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{x.member}.Sanitize()); err != nil {
			t.Error(err)
		}
		if _, err := x.targetRoot.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{x.member}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	if _, err = f.pool.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{x.member}.Sanitize()+" SET timezone TO 'Europe/Istanbul'"); err != nil {
		t.Fatal(err)
	}
	for _, prepare := range configure {
		prepare(x)
	}
	x.f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), x.f.lease.Operation)
	if err != nil || len(plans) != 1 {
		t.Fatalf("role worker frozen source: %v", err)
	}
	x.source = plans[0]
	cloneInventorySQLConnection(t, x.f, p)
	if _, err = f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), x.f.lease, x.source); err != nil {
		t.Fatal(err)
	}
	x.f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), x.f.lease)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.store = x.store
	owner, err := x.store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), x.f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := x.store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), x.f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	name := "grg_worker_target_" + uuid.NewString()[:12]
	if _, err = x.targetRoot.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := x.targetRoot.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg := x.targetRoot.Config().Copy()
	cfg.Database, cfg.Password = name, "bootstrap-fixture-password"
	cfg.RuntimeParams = map[string]string{"search_path": "pg_catalog", "default_transaction_read_only": "off"}
	x.target = copyarchive.RestoreTarget{Scope: inventory.Sealed.Scope, OwnerID: owner.TargetDatabaseID, ProviderResourceID: owner.ProviderResourceID, ProviderCreatedAt: owner.ProviderCreatedAt,
		DataResourceID: owner.ProviderResourceID + "/root", EndpointID: "ep-role-worker", EndpointCreatedAt: owner.ProviderCreatedAt, DatabaseName: name, RoleName: cfg.User}
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=$1 AND r.rolname=$2", name, cfg.User).Scan(&x.target.DatabaseOID, &x.target.RoleOID); err != nil {
		t.Fatal(err)
	}
	p.targetSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected != name {
			return nil, managedpostgres.ErrConflict
		}
		if x.beforeSQL != nil {
			if err := x.beforeSQL(ctx); err != nil {
				return nil, err
			}
		}
		conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
		if err == nil {
			x.connections = append(x.connections, conn)
		}
		return conn, err
	}
	sealed, err := copyarchive.SealTarget(setSecretRecipient(), x.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.store.RecordProjectEnvironmentClonePostgresTargetSQLPins(t.Context(), x.f.lease, id, inventory.Sealed.Fingerprint, sealed); err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *roleWorkerFixture) assertPrivate(t *testing.T) {
	t.Helper()
	var private bool
	if err := x.f.pool.QueryRow(t.Context(), "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", x.target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("role seeding published a dataset: %v", err)
	}
	for _, c := range x.connections {
		if !c.IsClosed() {
			t.Fatal("role worker retained its borrowed connection")
		}
	}
}

func (x *roleWorkerFixture) roleExists(t *testing.T) bool {
	t.Helper()
	var exists bool
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", x.member).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestPGClonePostgresRoleWorkerRetainsPlanBeforeDDLAndRecoversOriginalKey(t *testing.T) {
	x := cloneRoleWorkerFixture(t)
	x.store.loseRecord = true
	if got, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source); !errors.Is(err, managedpostgres.ErrUnavailable) || !got.SeededAt().IsZero() || x.p.targetSQLCalls != 1 || x.roleExists(t) {
		t.Fatalf("lost plan reply authorized role DDL: %v", err)
	}
	original, err := x.store.ProjectEnvironmentClonePostgresRolePlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, previous} }
	setSecretRecipient = nil
	service := x.f.srv.managedPostgres
	x.f.srv.managedPostgres = nil
	if _, owner, pins, err := x.f.srv.projectEnvironmentClonePostgresRolePlan(t.Context(), x.f.lease, x.source); err != nil || !sameClonePostgresRolePlan(owner, original) || pins != x.target || x.p.targetSQLCalls != 1 {
		t.Fatalf("plan recovery required provider/current recipient: %v", err)
	}
	x.f.srv.managedPostgres = service
	if err = x.store.ReleaseProjectEnvironmentCloneLease(t.Context(), x.f.lease, 0); err != nil {
		t.Fatal(err)
	}
	x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.f.pool.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{x.member}.Sanitize()+" SET timezone TO 'UTC'"); err != nil {
		t.Fatal(err)
	}
	r, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source)
	if err != nil || r.SeededAt().IsZero() || x.p.targetSQLCalls != 2 || !x.roleExists(t) {
		t.Fatalf("original role worker handoff: %v", err)
	}
	var targetOID, sourceOID uint32
	var login, noPassword bool
	var config []string
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT r.oid,r.rolcanlogin,a.rolpassword IS NULL,r.rolconfig FROM pg_roles r JOIN pg_authid a USING(oid) WHERE r.rolname=$1", x.member).Scan(&targetOID, &login, &noPassword, &config); err != nil || login || !noPassword || len(config) != 1 || config[0] != "TimeZone=Europe/Istanbul" {
		t.Fatalf("role worker changed original settings/copied login or password: %v (%v)", err, config)
	}
	if err = x.f.pool.QueryRow(t.Context(), "SELECT oid FROM pg_roles WHERE rolname=$1", x.member).Scan(&sourceOID); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r.IdentitiesForWorker() {
		if id.SourceOID == sourceOID {
			found = id.TargetOID == targetOID && id.DesiredLogin
		}
	}
	if !found {
		t.Fatal("role worker lost independent target OID/original login intent")
	}
	retry, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source)
	if err != nil || !retry.SeededAt().Equal(r.SeededAt()) || x.p.targetSQLCalls != 3 {
		t.Fatalf("exact role retry changed committed identity: %v", err)
	}
	retained, err := x.store.ProjectEnvironmentClonePostgresRolePlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil || !sameClonePostgresRolePlan(retained, original) || !bytes.Equal(retained.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("role worker rebased durable input: %v", err)
	}
	x.assertPrivate(t)
}

func TestPGClonePostgresRoleWorkerRecoversTargetCommitAfterProviderReplyLoss(t *testing.T) {
	x := cloneRoleWorkerFixture(t)
	if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresRolePlan(t.Context(), x.f.lease, x.source); err != nil {
		t.Fatal(err)
	}
	x.p.targetSQLAfterError = errors.New("lost provider reply after committed role DDL")
	if got, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source); err == nil || !got.SeededAt().IsZero() || !x.roleExists(t) {
		t.Fatalf("uncertain role commit published completion: %v", err)
	}
	var originalOID uint32
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_roles WHERE rolname=$1", x.member).Scan(&originalOID); err != nil {
		t.Fatal(err)
	}
	x.p.targetSQLAfterError = nil
	recovered, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source)
	if err != nil || recovered.SeededAt().IsZero() || x.p.targetSQLCalls != 3 {
		t.Fatalf("target role journal recovery: %v", err)
	}
	var oid uint32
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_roles WHERE rolname=$1", x.member).Scan(&oid); err != nil || oid != originalOID {
		t.Fatalf("unknown role commit repeated creation: %v", err)
	}
	x.assertPrivate(t)
}

func TestPGClonePostgresRoleWorkerRefusesUnreadablePlansAndBoundaryAuthorityDrift(t *testing.T) {
	for _, fault := range []string{"key", "damaged_plan", "source", "stale", "phase", "owner", "target_drift"} {
		t.Run(fault, func(t *testing.T) {
			x := cloneRoleWorkerFixture(t)
			if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresRolePlan(t.Context(), x.f.lease, x.source); err != nil {
				t.Fatal(err)
			}
			calls := 1
			switch fault {
			case "key":
				other, _ := age.GenerateX25519Identity()
				mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{other} }
			case "damaged_plan":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_role_plans SET ciphertext=decode('00','hex') WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "source":
				x.source.hash = strings.Repeat("f", 64)
			case "stale", "phase", "owner":
				x.beforeSQL = func(ctx context.Context) error {
					if fault == "stale" {
						return x.store.ReleaseProjectEnvironmentCloneLease(ctx, x.f.lease, 0)
					}
					query := "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1"
					id := x.f.lease.Operation.ID
					if fault == "owner" {
						query, id = "UPDATE managed_postgres_databases SET observed_generation=1 WHERE id=$1", x.target.OwnerID
					}
					_, err := x.f.pool.Exec(ctx, query, id)
					return err
				}
				calls = 2
			case "target_drift":
				if _, err := x.targetRoot.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{x.member}.Sanitize()); err != nil {
					t.Fatal(err)
				}
				calls = 2
			}
			got, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source)
			if err == nil || !got.SeededAt().IsZero() || x.p.targetSQLCalls != calls {
				t.Fatalf("unqualified role dispatch/recovery: %v, calls=%d", err, x.p.targetSQLCalls)
			}
			if fault != "target_drift" && x.roleExists(t) {
				t.Fatal("rejected worker committed a source role")
			}
			for _, c := range x.connections {
				if !c.IsClosed() {
					t.Fatal("rejected worker left a connection open")
				}
			}
		})
	}
}

func TestPGClonePostgresRoleWorkerRejectsUnopenableRecipientsAndTargetPostchecksBeforePlanCommit(t *testing.T) {
	for _, fault := range []string{"recipient", "unopenable", "postcheck", "different_existing"} {
		t.Run(fault, func(t *testing.T) {
			x := cloneRoleWorkerFixture(t)
			calls := 0
			switch fault {
			case "recipient":
				setSecretRecipient = nil
			case "unopenable":
				other, _ := age.GenerateX25519Identity()
				setSecretRecipient = func() *age.X25519Recipient { return other.Recipient() }
			case "postcheck":
				x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
				calls = 1
			case "different_existing":
				if _, err := x.targetRoot.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{x.member}.Sanitize()+" LOGIN INHERIT"); err != nil {
					t.Fatal(err)
				}
				calls = 1
			}
			got, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source)
			if err == nil || !got.SeededAt().IsZero() || x.p.targetSQLCalls != calls {
				t.Fatalf("unqualified role plan capture: %v", err)
			}
			if _, err := x.store.ProjectEnvironmentClonePostgresRolePlanForLease(t.Context(), x.f.lease, x.source.source.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("failed plan capture retained ownership: %v", err)
			}
			x.assertPrivate(t)
		})
	}
}
