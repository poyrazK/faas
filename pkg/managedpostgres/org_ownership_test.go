package managedpostgres

// adr: 202610171906

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgPostgresQuotaAndUsageAcrossNamespaces(t *testing.T) {
	catalog, pool, ctx, creator := postgresStoreFixture(t)
	identities := state.NewPgStore(pool)
	org, err := identities.CreateOrg(ctx, state.Org{Slug: "database-quota", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	// Free organizations adopt resource ownership immediately. This exercises
	// the storage invariant independently of HTTP plan admission.
	ownerCtx := WithOwnership(ctx, org.ID, org.ID)
	at := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	personal := postgresTestDatabase(creator, "personal", at)
	if _, _, err := catalog.Reserve(ctx, personal, 1); err != nil {
		t.Fatal(err)
	}
	first := postgresTestDatabase(creator, "company-one", at)
	first.OrgID = org.ID
	database, created, err := catalog.Reserve(ownerCtx, first, 2)
	if err != nil || !created {
		t.Fatalf("first company reservation: %v", err)
	}
	var admitted, denied, unexpected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			d := postgresTestDatabase(org.ID, "company-"+uuid.NewString()[:8], at)
			d.OrgID = org.ID
			_, _, err := catalog.Reserve(ownerCtx, d, 2)
			switch {
			case err == nil:
				admitted.Add(1)
			case errors.Is(err, ErrQuotaExceeded):
				denied.Add(1)
			default:
				unexpected.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 || denied.Load() != 7 || unexpected.Load() != 0 {
		t.Fatalf("company quota: admitted=%d denied=%d unexpected=%d", admitted.Load(), denied.Load(), unexpected.Load())
	}
	rows, err := catalog.ListCustomerDatabases(ownerCtx, org.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("company inventory: %d %v", len(rows), err)
	}
	if err := identities.SoftDeleteOrg(ctx, org.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("organization deletion orphaned database: %v", err)
	}
	// Company databases no longer consume the creator's personal slots.
	secondPersonal := postgresTestDatabase(creator, "personal-two", at)
	if _, _, err := catalog.Reserve(ctx, secondPersonal, 2); err != nil {
		t.Fatalf("company consumed personal quota: %v", err)
	}
	for i, d := range rows {
		claimed, err := catalog.Claim(ctx, d.AccountID, d.ID, uuid.NewString(), StateProvisioning, at, at.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.RecordProviderResource(ctx, d.ID, claimed.LeaseToken, "provider-"+d.ID, at); err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.FinishProvision(ctx, d.ID, claimed.LeaseToken, at); err != nil {
			t.Fatal(err)
		}
		records := []UsageRecord{{AccountID: d.AccountID, DatabaseID: d.ID, BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint, WindowFrom: at, WindowTo: at.Add(time.Hour), ObservedAt: at.Add(time.Hour), Meter: MeterComputeUnitSeconds, Quantity: int64(10 + i), CostMillicents: int64(100 + i)}}
		if err := catalog.RecordUsage(ctx, records); err != nil {
			t.Fatal(err)
		}
	}
	total, err := catalog.UsageSnapshot(ctx, org.ID, at)
	if err != nil || total.ComputeUnitSeconds != 21 || total.CostMillicents != 201 || total.ReadyDatabases != 2 {
		t.Fatalf("company usage: %+v %v", total, err)
	}
	personalUsage, err := catalog.UsageSnapshot(ctx, creator, at)
	if err != nil || personalUsage.ComputeUnitSeconds != 0 || len(personalUsage.Databases) != 0 {
		t.Fatalf("personal usage included company: %+v %v", personalUsage, err)
	}
	// A persisted root cannot be changed by raw SQL.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET org_id=(SELECT id FROM orgs WHERE personal_owner_account_id=$1) WHERE id=$2`, creator, database.ID); err == nil {
		t.Fatal("database org was mutable")
	}
	// Erase a creator even when no company app retains its namespace. These
	// personal reservations never reached a provider, so their catalogue-only
	// deletion can complete directly.
	for _, id := range []string{personal.ID, secondPersonal.ID} {
		deletedAt := time.Now().UTC()
		claimed, err := catalog.ClaimDelete(ctx, creator, id, uuid.NewString(), deletedAt, deletedAt.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.FinishDelete(ctx, id, claimed.LeaseToken, deletedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := identities.MarkAccountDeletionPending(ctx, creator); err != nil {
		t.Fatal(err)
	}
	if err := identities.DeleteAccount(ctx, creator); err != nil {
		t.Fatal(err)
	}
	after, err := catalog.UsageSnapshot(ctx, org.ID, at)
	if err != nil || after.ComputeUnitSeconds != total.ComputeUnitSeconds || after.CostMillicents != total.CostMillicents || after.ReadyDatabases != 2 {
		t.Fatalf("creator erasure changed company accounting: %+v %v", after, err)
	}
	retained, err := catalog.Get(ctx, creator, database.ID)
	if err != nil || retained.OrgID != org.ID || retained.AccountID != creator || retained.State != StateReady {
		t.Fatalf("database-only legacy namespace was erased: %+v %v", retained, err)
	}

}

func TestOrgPostgresIntentFencesRevokedMembership(t *testing.T) {
	catalog, pool, ctx, owner := postgresStoreFixture(t)
	identities := state.NewPgStore(pool)
	org, err := identities.CreateOrg(ctx, state.Org{Slug: "database-fence", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identities.CreateAccount(ctx, uuid.NewString()+"@database-fence.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner, state.OrgRoleOwner}, {actor.ID, state.OrgRoleAdmin}} {
		if err := identities.AddOrgMember(ctx, org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := identities.OrgAccessSnapshot(ctx, org.ID, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	scoped := WithOwnership(ctx, org.ID, org.ID)
	fenced, cancel := state.WithOrgAccessFence(scoped, state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	if err := identities.RemoveOrgMember(ctx, org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	input := postgresTestDatabase(org.ID, "revoked", time.Now().UTC())
	input.OrgID = org.ID
	if _, _, err := catalog.Reserve(fenced, input, 2); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("removed member reserved a database: %v", err)
	}
	if _, err := catalog.DatabaseOwnership(ctx, input.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied intent committed: %v", err)
	}
	second, err := identities.CreateAccount(ctx, uuid.NewString()+"@ordered-database-fence.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := identities.AddOrgMember(ctx, org.ID, second.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	current, err := identities.OrgAccessSnapshot(ctx, org.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	ordered, stop := state.WithOrgAccessFence(scoped, state.OrgAccessFence{OrgID: org.ID, AccountID: second.ID, Revision: current.Revision})
	defer stop(nil)
	removal, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = removal.Rollback(ctx) }()
	if _, err := removal.Exec(ctx, `SELECT account_id FROM org_memberships WHERE org_id=$1 AND account_id=$2 FOR UPDATE`, org.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	entered := make(chan struct{})
	go func() { close(entered); _, _, err := catalog.Reserve(ordered, input, 2); result <- err }()
	<-entered
	select {
	case err := <-result:
		t.Fatalf("reservation passed removal lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := removal.Exec(ctx, `UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2`, org.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := removal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrOrgAccessRevoked) {
			t.Fatalf("reservation survived removal commit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reservation did not resume after removal")
	}

}

func TestOrgPostgresMemoryFenceSharesPromotionLockOrder(t *testing.T) {
	ctx := t.Context()
	identities := state.NewMemStore()
	actor, err := identities.CreateAccount(ctx, "promotion-order@database.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := identities.CreateOrg(ctx, state.Org{Slug: "promotion-database-order", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if err := identities.AddOrgMember(ctx, org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := identities.OrgAccessSnapshot(ctx, org.ID, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	scoped := WithOwnership(ctx, org.ID, org.ID)
	fenced, cancel := state.WithOrgAccessFence(scoped, state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	fenced = identities.WithMemoryIntentFence(fenced)
	catalog := NewMemoryStore()
	appID := uuid.NewString()
	fence, err := catalog.ReadPromotionFence(ctx, org.ID, appID)
	if err != nil {
		t.Fatal(err)
	}
	held, started := make(chan struct{}), make(chan struct{})
	promotion, mutation := make(chan error, 1), make(chan error, 1)
	go func() {
		promotion <- catalog.GuardPromotion(ctx, org.ID, appID, fence, nil, func(ctx context.Context) error {
			close(held)
			<-started
			time.Sleep(30 * time.Millisecond)
			_, err := identities.OrgByID(ctx, org.ID)
			return err
		})
	}()
	<-held
	go func() {
		close(started)
		input := postgresTestDatabase(org.ID, "concurrent-reservation", time.Now().UTC())
		input.OrgID = org.ID
		_, _, err := catalog.Reserve(fenced, input, 2)
		mutation <- err
	}()
	for _, result := range []<-chan error{promotion, mutation} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("catalogue and membership locks deadlocked with promotion")
		}
	}
}
