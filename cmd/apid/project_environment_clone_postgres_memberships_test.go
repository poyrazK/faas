//go:build !no_pg

// adr:567
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
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/state"
)

type membershipWorkerFailureStore struct {
	*roleWorkerFailureStore
	loseMembershipRecord bool
}

func (s *membershipWorkerFailureStore) RecordProjectEnvironmentClonePostgresMembershipPlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, parentSHA256 string, sealed copyroles.SealedMemberships) (state.ProjectEnvironmentClonePostgresMembershipPlan, bool, error) {
	r, first, err := s.PgStore.RecordProjectEnvironmentClonePostgresMembershipPlan(ctx, l, id, parentSHA256, sealed)
	if err == nil && s.loseMembershipRecord {
		s.loseMembershipRecord = false
		return state.ProjectEnvironmentClonePostgresMembershipPlan{}, false, managedpostgres.ErrUnavailable
	}
	return r, first, err
}

func cloneMembershipWorkerFixture(t *testing.T) (*roleWorkerFixture, *membershipWorkerFailureStore) {
	t.Helper()
	x := cloneRoleWorkerFixture(t, func(x *roleWorkerFixture) {
		// Freeze a real source grant before the original inventory is encrypted.
		// The independent target has no such grant; bootstrap is an administrator
		// in this composition fixture. Ordinary-owner graph tests live in copyroles.
		if _, err := x.f.pool.Exec(t.Context(), "GRANT pg_read_all_data TO "+pgx.Identifier{x.member}.Sanitize()+" WITH ADMIN FALSE, INHERIT FALSE, SET TRUE"); err != nil {
			t.Fatal(err)
		}
	})
	s := &membershipWorkerFailureStore{roleWorkerFailureStore: x.store}
	x.f.srv.store = s
	return x, s
}

func workerMembershipCount(t *testing.T, x *roleWorkerFixture) int {
	t.Helper()
	var count int
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE r.rolname=$1", x.member).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertWorkerMembership(t *testing.T, x *roleWorkerFixture) {
	t.Helper()
	var exact bool
	if err := x.targetRoot.QueryRow(t.Context(), `SELECT m.grantor=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND NOT m.admin_option AND NOT m.inherit_option AND m.set_option AND NOT r.rolcanlogin AND a.rolpassword IS NULL FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member JOIN pg_authid a ON a.oid=r.oid JOIN pg_roles g ON g.oid=m.roleid WHERE r.rolname=$1 AND g.rolname='pg_read_all_data'`, x.member).Scan(&exact); err != nil || !exact || workerMembershipCount(t, x) != 1 {
		t.Fatalf("worker changed original grantor/options or activated credentials: %v", err)
	}
	x.assertPrivate(t)
}

func TestPGClonePostgresMembershipWorkerRetainsBeforeGrantsAndRecoversOriginalGraphAcrossHandoff(t *testing.T) {
	x, s := cloneMembershipWorkerFixture(t)
	s.loseMembershipRecord = true
	if got, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source); !errors.Is(err, managedpostgres.ErrUnavailable) || !got.AppliedAt().IsZero() || x.p.targetSQLCalls != 3 || !x.roleExists(t) || workerMembershipCount(t, x) != 0 {
		t.Fatalf("lost graph record authorized grants: %v (calls=%d)", err, x.p.targetSQLCalls)
	}
	original, err := s.ProjectEnvironmentClonePostgresMembershipPlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, previous} }
	setSecretRecipient = nil
	service := x.f.srv.managedPostgres
	x.f.srv.managedPostgres = nil
	if _, owner, target, err := x.f.srv.projectEnvironmentClonePostgresMembershipPlan(t.Context(), x.f.lease, x.source); err != nil || !sameClonePostgresMembershipPlan(owner, original) || target != x.target || x.p.targetSQLCalls != 3 {
		t.Fatalf("graph recovery required SQL/provider/current recipient: %v", err)
	}
	x.f.srv.managedPostgres = service
	old := x.f.lease
	if err = s.ReleaseProjectEnvironmentCloneLease(t.Context(), old, 0); err != nil {
		t.Fatal(err)
	}
	x.f.lease, err = s.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), old, x.source); !errors.Is(err, state.ErrConflict) || !got.AppliedAt().IsZero() || x.p.targetSQLCalls != 3 {
		t.Fatalf("stale worker dispatched grants: %v", err)
	}
	for _, query := range []string{
		"ALTER ROLE " + pgx.Identifier{x.member}.Sanitize() + " SET timezone TO 'UTC'",
		"GRANT pg_read_all_data TO " + pgx.Identifier{x.member}.Sanitize() + " WITH ADMIN FALSE, INHERIT TRUE, SET FALSE",
	} {
		if _, err = x.f.pool.Exec(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	r, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source)
	if err != nil || r.AppliedAt().IsZero() || x.p.targetSQLCalls != 4 {
		t.Fatalf("original membership handoff: %v (calls=%d)", err, x.p.targetSQLCalls)
	}
	assertWorkerMembership(t, x)
	retry, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source)
	if err != nil || !retry.AppliedAt().Equal(r.AppliedAt()) || x.p.targetSQLCalls != 5 {
		t.Fatalf("membership retry replaced target journal: %v", err)
	}
	retained, err := s.ProjectEnvironmentClonePostgresMembershipPlanForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil || !sameClonePostgresMembershipPlan(retained, original) {
		t.Fatalf("membership worker rebased graph ownership: %v", err)
	}
	assertWorkerMembership(t, x)
}

func TestPGClonePostgresMembershipWorkerRecoversCommittedTargetAfterProviderReplyLoss(t *testing.T) {
	x, _ := cloneMembershipWorkerFixture(t)
	if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresMembershipPlan(t.Context(), x.f.lease, x.source); err != nil {
		t.Fatal(err)
	}
	var originalOID uint32
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_roles WHERE rolname=$1", x.member).Scan(&originalOID); err != nil {
		t.Fatal(err)
	}
	x.p.targetSQLAfterError = errors.New("lost provider reply after committed grants")
	if got, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source); err == nil || !got.AppliedAt().IsZero() || x.p.targetSQLCalls != 4 || workerMembershipCount(t, x) != 1 {
		t.Fatalf("uncertain membership commit published completion: %v", err)
	}
	cfg := x.targetRoot.Config().Copy()
	cfg.Database = x.target.DatabaseName
	c, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var at time.Time
	if err = c.QueryRow(t.Context(), "SELECT applied_at FROM gregale_copy_memberships.receipt").Scan(&at); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	x.p.targetSQLAfterError = nil
	r, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source)
	if err != nil || !r.AppliedAt().Equal(at) || x.p.targetSQLCalls != 5 {
		t.Fatalf("unknown membership commit recreated graph: %v", err)
	}
	var oid uint32
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_roles WHERE rolname=$1", x.member).Scan(&oid); err != nil || oid != originalOID {
		t.Fatalf("membership recovery recreated original role: %v", err)
	}
	assertWorkerMembership(t, x)
}

func TestPGClonePostgresMembershipWorkerRejectsUnreadableParentsAndAuthorityOrTargetDrift(t *testing.T) {
	for _, fault := range []string{"key", "damaged_graph", "role_parent", "source", "stale", "phase", "owner", "target_drift"} {
		t.Run(fault, func(t *testing.T) {
			x, _ := cloneMembershipWorkerFixture(t)
			if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresMembershipPlan(t.Context(), x.f.lease, x.source); err != nil {
				t.Fatal(err)
			}
			calls := 3
			switch fault {
			case "key":
				other, _ := age.GenerateX25519Identity()
				mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{other} }
			case "damaged_graph", "role_parent":
				table := "project_environment_clone_postgres_membership_plans"
				if fault == "role_parent" {
					table = "project_environment_clone_postgres_role_plans"
				}
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE "+table+" SET ciphertext=decode('00','hex') WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "source":
				x.source.hash = strings.Repeat("f", 64)
			case "stale", "phase", "owner":
				x.beforeSQL = func(ctx context.Context) error {
					if fault == "stale" {
						return x.store.ReleaseProjectEnvironmentCloneLease(ctx, x.f.lease, 0)
					}
					query, id := "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", x.f.lease.Operation.ID
					if fault == "owner" {
						query, id = "UPDATE managed_postgres_databases SET observed_generation=1 WHERE id=$1", x.target.OwnerID
					}
					_, err := x.f.pool.Exec(ctx, query, id)
					return err
				}
				calls = 4
			case "target_drift":
				if _, err := x.targetRoot.Exec(t.Context(), "GRANT pg_read_all_data TO "+pgx.Identifier{x.member}.Sanitize()+" WITH ADMIN FALSE, INHERIT TRUE, SET FALSE"); err != nil {
					t.Fatal(err)
				}
				calls = 4
			}
			got, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source)
			if err == nil || !got.AppliedAt().IsZero() || x.p.targetSQLCalls != calls {
				t.Fatalf("unqualified membership dispatch: %v (calls=%d)", err, x.p.targetSQLCalls)
			}
			if fault != "target_drift" && workerMembershipCount(t, x) != 0 {
				t.Fatal("rejected worker committed membership")
			}
			for _, c := range x.connections {
				if !c.IsClosed() {
					t.Fatal("rejected membership worker retained its connection")
				}
			}
		})
	}
}

func TestPGClonePostgresMembershipWorkerChecksRecipientsAndPostchecksBeforeGraphCommit(t *testing.T) {
	for _, fault := range []string{"recipient", "unopenable", "postcheck"} {
		t.Run(fault, func(t *testing.T) {
			x, s := cloneMembershipWorkerFixture(t)
			if _, err := x.f.srv.projectEnvironmentClonePostgresRoles(t.Context(), x.f.lease, x.source); err != nil {
				t.Fatal(err)
			}
			calls := 2
			switch fault {
			case "recipient":
				setSecretRecipient = nil
			case "unopenable":
				other, _ := age.GenerateX25519Identity()
				setSecretRecipient = func() *age.X25519Recipient { return other.Recipient() }
			case "postcheck":
				// Existing role seeding recovers first; fail the subsequent graph
				// catalogue borrow after it has returned to the provider borrower.
				x.beforeSQL = func(context.Context) error {
					if x.p.targetSQLCalls == 4 {
						x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
					}
					return nil
				}
				calls = 4
			}
			got, err := x.f.srv.projectEnvironmentClonePostgresMemberships(t.Context(), x.f.lease, x.source)
			if err == nil || !got.AppliedAt().IsZero() || x.p.targetSQLCalls != calls || workerMembershipCount(t, x) != 0 {
				t.Fatalf("unqualified membership capture: %v (calls=%d)", err, x.p.targetSQLCalls)
			}
			if _, err := s.ProjectEnvironmentClonePostgresMembershipPlanForLease(t.Context(), x.f.lease, x.source.source.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("failed graph capture retained ownership: %v", err)
			}
			x.assertPrivate(t)
		})
	}
}
