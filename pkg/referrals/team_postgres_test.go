package referrals_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func addAccount(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email) VALUES ($1,$2)", id, email); err != nil {
		t.Fatal(err)
	}
	return id
}

func payInvoice(t *testing.T, pool *pgxpool.Pool, subject string, subtotal int64) {
	t.Helper()
	invoice := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents)
 VALUES ($1,$2,'paid','usd',$3,$3,0)`, invoice, subject, subtotal); err != nil {
		t.Fatal(err)
	}
	if err := referrals.RecordPayment(t.Context(), pool, invoice); err != nil {
		t.Fatal(err)
	}
}

func onlyRecord(t *testing.T, store *referrals.PostgresStore, owner string, all bool) referrals.Record {
	t.Helper()
	rows, err := store.List(t.Context(), referrals.Scope{OwnerID: owner, All: all}, "", 25)
	if err != nil || len(rows) != 1 {
		t.Fatalf("records: %+v %v", rows, err)
	}
	return rows[0]
}

func TestAgreementIsCapturedPerSalespersonAndNeverRewritten(t *testing.T) {
	pool, store, link, customer := checkoutFixture(t)
	ctx := t.Context()
	owner := link.OwnerID
	if _, err := store.AddAgreement(ctx, owner, owner, referrals.AgreementInput{CommissionBPS: 2000, EffectiveFrom: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	// A future agreement does not apply yet.
	if _, err := store.AddAgreement(ctx, owner, owner, referrals.AgreementInput{CommissionBPS: 5000, EffectiveFrom: time.Now().Add(48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if bps, err := store.CurrentCommission(ctx, owner, 1250); err != nil || bps != 2000 {
		t.Fatal("current agreement", bps, err)
	}
	if created, err := store.AttributeCheckout(ctx, customer, checkoutTerms(link.Code), []string{"rep@example.com"}); err != nil || !created {
		t.Fatal(created, err)
	}
	if _, err := store.AddAgreement(ctx, owner, owner, referrals.AgreementInput{CommissionBPS: 100, EffectiveFrom: time.Now()}); err != nil {
		t.Fatal(err)
	}
	payInvoice(t, pool, customer, 1000)
	r := onlyRecord(t, store, owner, false)
	if r.CommissionBPS != 2000 || r.RewardCents != 200 {
		t.Fatalf("captured terms changed: %+v", r)
	}
	// Without any agreement the program default applies.
	other := addAccount(t, pool, "rep2@example.com")
	if bps, err := store.CurrentCommission(ctx, other, 1250); err != nil || bps != 1250 {
		t.Fatal("default", bps, err)
	}
	history, err := store.Agreements(ctx, owner)
	if err != nil || len(history) != 3 || history[0].CommissionBPS != 5000 {
		t.Fatalf("history %+v %v", history, err)
	}
}

func TestTeamMembersUseVersionsAndInactiveRowsLeaveTheRoster(t *testing.T) {
	pool, store, _, _ := checkoutFixture(t)
	ctx := t.Context()
	admin := addAccount(t, pool, "boss@example.com")
	rep := addAccount(t, pool, "Seller@Example.com")
	if id, err := store.AccountIDByEmail(ctx, "seller@example.com"); err != nil || id != rep {
		t.Fatal("lookup", id, err)
	}
	if _, err := store.AccountIDByEmail(ctx, "nobody@example.com"); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.SaveTeamMember(ctx, admin, rep, referrals.RoleRep, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTeamMember(ctx, admin, rep, referrals.RoleRep, true, 0); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("duplicate insert", err)
	}
	roster, err := store.TeamRoster(ctx)
	if err != nil || roster["seller@example.com"] != referrals.RoleRep {
		t.Fatal(roster, err)
	}
	if err = store.SaveTeamMember(ctx, admin, rep, referrals.RoleManager, true, 1); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTeamMember(ctx, admin, rep, referrals.RoleRep, false, 1); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale version", err)
	}
	if err = store.SaveTeamMember(ctx, admin, rep, referrals.RoleManager, false, 2); err != nil {
		t.Fatal(err)
	}
	if roster, err = store.TeamRoster(ctx); err != nil || roster["seller@example.com"] != "" {
		t.Fatal("inactive member stayed", roster, err)
	}
	members, err := store.TeamMembers(ctx, []string{"rep@example.com"})
	if err != nil || len(members) != 2 {
		t.Fatalf("members %+v %v", members, err)
	}
	for _, m := range members {
		if m.Email == "Seller@Example.com" && (m.StoredRole != referrals.RoleManager || m.StoredActive || m.Version != 3 || m.CommissionBPS != nil) {
			t.Fatalf("stored member %+v", m)
		}
	}
}

func TestCreditMovesToNewOwnerRateUntilAPayoutIsRecorded(t *testing.T) {
	pool, store, link, customer := checkoutFixture(t)
	ctx := t.Context()
	from := link.OwnerID
	admin := addAccount(t, pool, "boss@example.com")
	to := addAccount(t, pool, "closer@example.com")
	if _, err := store.AddAgreement(ctx, admin, to, referrals.AgreementInput{CommissionBPS: 3000, EffectiveFrom: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttributeCheckout(ctx, customer, checkoutTerms(link.Code), []string{"rep@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveCredit(ctx, admin, customer, from, "noop", 1250); !errors.Is(err, referrals.ErrSameOwner) {
		t.Fatal("same owner", err)
	}
	change, err := store.MoveCredit(ctx, admin, customer, to, "customer used the wrong code", 1250)
	if err != nil || change.FromOwnerID != from || change.FromCommissionBPS != 1250 || change.ToCommissionBPS != 3000 {
		t.Fatalf("move %+v %v", change, err)
	}
	if rows, err := store.List(ctx, referrals.Scope{OwnerID: from}, "", 25); err != nil || len(rows) != 0 {
		t.Fatal("old owner still sees the customer", rows, err)
	}
	payInvoice(t, pool, customer, 1000)
	r := onlyRecord(t, store, to, false)
	if r.CommissionBPS != 3000 || r.RewardCents != 300 || r.RewardStatus != "eligible" {
		t.Fatalf("moved record %+v", r)
	}
	history, err := store.CreditHistory(ctx, customer)
	if err != nil || len(history) != 1 || history[0].FromEmail != "rep@example.com" || history[0].ToEmail != "closer@example.com" || history[0].ChangedByEmail != "boss@example.com" {
		t.Fatalf("history %+v %v", history, err)
	}
	if _, err = store.RecordPayout(ctx, admin, customer, referrals.PayoutInput{AmountCents: 300, PaidOn: "2026-11-20", Reference: "wire-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.MoveCredit(ctx, admin, customer, from, "after payout", 1250); !errors.Is(err, referrals.ErrPayoutRecorded) {
		t.Fatal("moved paid credit", err)
	}
}

func TestCustomerReferralsCannotBecomeSalesCredit(t *testing.T) {
	pool, store, owner, _ := fixture(t)
	ctx := t.Context()
	link, err := store.EnsureLink(ctx, owner, "customer")
	if err != nil {
		t.Fatal(err)
	}
	customer := attribute(ctx, t, pool, link, referrals.Policy{Currency: "usd", ConversionDays: 90})
	rep := addAccount(t, pool, "rep3@example.com")
	if _, err = store.MoveCredit(ctx, owner, customer, rep, "convert", 0); !errors.Is(err, referrals.ErrNotSalesCredit) {
		t.Fatal(err)
	}
	if _, err = store.RecordPayout(ctx, owner, customer, referrals.PayoutInput{AmountCents: 1, PaidOn: "2026-11-20"}); !errors.Is(err, referrals.ErrNotSalesCredit) {
		t.Fatal(err)
	}
}

func TestPayoutLedgerRequiresEligibilityAndAllowsOneLivePayout(t *testing.T) {
	pool, store, link, customer := checkoutFixture(t)
	ctx := t.Context()
	admin := addAccount(t, pool, "boss@example.com")
	if _, err := store.AttributeCheckout(ctx, customer, checkoutTerms(link.Code), []string{"rep@example.com"}); err != nil {
		t.Fatal(err)
	}
	in := referrals.PayoutInput{AmountCents: 125, PaidOn: "2026-11-20", Reference: "wire-7", Note: "November run"}
	if _, err := store.RecordPayout(ctx, admin, customer, in); !errors.Is(err, referrals.ErrPayoutNotEligible) {
		t.Fatal("paid before eligible", err)
	}
	payInvoice(t, pool, customer, 1000)
	payout, err := store.RecordPayout(ctx, admin, customer, in)
	if err != nil || payout.OwnerID != link.OwnerID || payout.Currency != "usd" {
		t.Fatalf("payout %+v %v", payout, err)
	}
	if _, err = store.RecordPayout(ctx, admin, customer, in); !errors.Is(err, referrals.ErrPayoutRecorded) {
		t.Fatal("second live payout", err)
	}
	r := onlyRecord(t, store, link.OwnerID, false)
	if r.PayoutCents != 125 || r.PayoutPaidOn != "2026-11-20" {
		t.Fatalf("record payout fields %+v", r)
	}
	// Salespeople see only their own payouts; managers see everyone's.
	if rows, err := store.Payouts(ctx, referrals.Scope{OwnerID: admin}, "", false); err != nil || len(rows) != 0 {
		t.Fatal("other rep saw payout", rows, err)
	}
	rows, err := store.Payouts(ctx, referrals.Scope{OwnerID: admin, All: true}, "", false)
	if err != nil || len(rows) != 1 || rows[0].OwnerEmail != "rep@example.com" || rows[0].RecordedByEmail != "boss@example.com" || rows[0].Reference != "wire-7" {
		t.Fatalf("manager payouts %+v %v", rows, err)
	}
	if ok, err := store.VoidPayout(ctx, admin, payout.ID, "wrong amount"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := store.VoidPayout(ctx, admin, payout.ID, "again"); err != nil || ok {
		t.Fatal("voided twice", ok, err)
	}
	if rows, err = store.Payouts(ctx, referrals.Scope{All: true}, "", false); err != nil || len(rows) != 0 {
		t.Fatal("voided payout listed", rows, err)
	}
	if rows, err = store.Payouts(ctx, referrals.Scope{All: true}, "", true); err != nil || len(rows) != 1 || rows[0].VoidReason != "wrong amount" {
		t.Fatal("voided payout hidden", rows, err)
	}
	in.AmountCents = 120
	if _, err = store.RecordPayout(ctx, admin, customer, in); err != nil {
		t.Fatal("re-record after void", err)
	}
}

func TestOrganizationFirstPurchaseCarriesSalesCredit(t *testing.T) {
	pool, store, link, buyer := checkoutFixture(t)
	ctx := t.Context()
	org, personal, paid := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs(id,name,personal_org) VALUES ($1,'Acme Corp',false),($2,'Personal',true),($3,'Paid Co',false)`, org, personal, paid); err != nil {
		t.Fatal(err)
	}
	if status, err := store.OrgCheckoutState(ctx, personal); err != nil || status.Eligible {
		t.Fatal("personal org eligible", status, err)
	}
	payInvoice(t, pool, paid, 500)
	if status, err := store.OrgCheckoutState(ctx, paid); err != nil || status.Reason != "not_first_purchase" {
		t.Fatal("paid org", status, err)
	}
	terms := checkoutTerms(link.Code)
	terms.SubjectKind, terms.SelectedBy = referrals.SubjectOrg, buyer
	if _, err := store.AttributeCheckout(ctx, paid, terms, []string{"rep@example.com"}); !errors.Is(err, referrals.ErrCheckoutIneligible) {
		t.Fatal(err)
	}
	// A salesperson cannot credit their own purchase for a company.
	self := terms
	self.SelectedBy = link.OwnerID
	if _, err := store.AttributeCheckout(ctx, org, self, []string{"rep@example.com"}); !errors.Is(err, referrals.ErrInvalidCheckoutCode) {
		t.Fatal("self org referral", err)
	}
	created, err := store.AttributeCheckout(ctx, org, terms, []string{"rep@example.com"})
	if err != nil || !created {
		t.Fatal(created, err)
	}
	if status, err := store.OrgCheckoutState(ctx, org); err != nil || status.Reason != "already_attributed" {
		t.Fatal(status, err)
	}
	// The member's personal account stays free to take its own referral.
	if status, err := store.CheckoutState(ctx, buyer); err != nil || !status.Eligible {
		t.Fatal("buyer account consumed", status, err)
	}
	payInvoice(t, pool, org, 2000)
	r := onlyRecord(t, store, link.OwnerID, false)
	if r.SubjectKind != referrals.SubjectOrg || r.OrgName != "Acme Corp" || r.Email != "other@example.com" || !r.Paid || r.RewardCents != 250 || r.RewardStatus != "eligible" {
		t.Fatalf("org record %+v", r)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM orgs WHERE id=$1", org); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.List(ctx, referrals.Scope{OwnerID: link.OwnerID}, "", 25); err != nil || len(rows) != 0 {
		t.Fatal("deleted org kept attribution", rows, err)
	}
}

func TestDeletingAnAccountStillErasesItsAttribution(t *testing.T) {
	pool, store, link, customer := checkoutFixture(t)
	ctx := t.Context()
	if _, err := store.AttributeCheckout(ctx, customer, checkoutTerms(link.Code), []string{"rep@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", customer); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM referral_attributions").Scan(&n); err != nil || n != 0 {
		t.Fatal("attribution survived account deletion", n, err)
	}
}
