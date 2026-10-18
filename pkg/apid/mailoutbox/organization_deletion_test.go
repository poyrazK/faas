package mailoutbox

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A notification transaction must neither bypass company ownership nor make
// its personal intent an owner of company resources after identity erasure.
func TestPGProducerOutboxCompanyDeletionOwnershipFence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "mail-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAccount(ctx, "mail-creator@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "mail-company", Name: "Mail Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct{ id, role string }{{owner.ID, state.OrgRoleOwner}, {creator.ID, state.OrgRoleDeveloper}} {
		if err := store.AddOrgMember(ctx, org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "mail-company-app", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_mail_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_mail_company"); err != nil {
		t.Fatal(err)
	}
	sloID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO app_slos(id,account_id,app_id,name,sli,objective_bp,window_days)
VALUES ($1,$2,$3,'unreviewed','availability',9900,7)`, sloID, creator.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	env := testEnv()
	service, err := NewFromEnv(pool, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	err = service.ScheduleDeletion(ctx, creator.ID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "account_shared_app_ownership_pending" {
		t.Fatalf("outbox bypassed company ownership: %v", err)
	}
	var status string
	var intents int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1) FROM accounts WHERE id=$1`, creator.ID).Scan(&status, &intents); err != nil || status != "active" || intents != 0 {
		t.Fatalf("blocked outbox transition changed state: %s %d %v", status, intents, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM app_slos WHERE id=$1`, sloID); err != nil {
		t.Fatal(err)
	}
	if err := service.ScheduleDeletion(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1`, creator.ID).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("allowed deletion lost its notification intent: %d %v", intents, err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1`, creator.ID).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("identity erasure retained personal notification intent: %d %v", intents, err)
	}
	saved, err := store.AppByID(ctx, app.ID)
	if err != nil || saved.AccountID != app.AccountID || saved.OrgID != org.ID {
		t.Fatalf("personal notification cleanup changed company app: %+v %v", saved, err)
	}
}
