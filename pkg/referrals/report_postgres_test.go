package referrals_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func reportFor(t *testing.T, store *referrals.PostgresStore, owner string, f referrals.ReportFilter) referrals.ManagerReport {
	t.Helper()
	r, err := store.ManagerReport(t.Context(), referrals.Scope{OwnerID: owner, All: true}, f)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func reportRow(t *testing.T, r referrals.ManagerReport, id string) referrals.RepComparison {
	t.Helper()
	for _, row := range r.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing salesperson %s", id)
	return referrals.RepComparison{}
}

func TestManagerReportCountsCollectionsAndCredit(t *testing.T) {
	pool, store, owner, other := pipelineFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	newcomer := uuid.NewString()
	if _, err := pool.Exec(ctx, "INSERT INTO accounts(id,email,mfa_enrolled_at) VALUES ($1,'new@example.com',now())", newcomer); err != nil {
		t.Fatal(err)
	}
	won := prospect(owner, "won@example.com")
	won.Stage = "won"
	wonID, err := store.SaveProspect(ctx, scope, owner, "", won)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []referrals.ProspectInput{prospect(owner, "open@example.com"), prospect(other, "other@example.org"), prospect(owner, "old@example.com")} {
		if _, err = store.SaveProspect(ctx, scope, owner, "", in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, "UPDATE sales_leads SET created_at='2026-11-03T12:00:00Z'; UPDATE sales_leads SET created_at='2026-10-31T23:00:00Z' WHERE email='old@example.com'"); err != nil {
		t.Fatal(err)
	}
	sale, err := store.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	otherSale, err := store.EnsureLink(ctx, other, "sales")
	if err != nil {
		t.Fatal(err)
	}
	customer, err := store.EnsureLink(ctx, owner, "customer")
	if err != nil {
		t.Fatal(err)
	}
	policy := referrals.Policy{Currency: "usd", CommissionBPS: 1000, ConversionDays: 90}
	add := func(link referrals.Link, currency string, paid, refunded, pending int64, missing bool) string {
		t.Helper()
		account := attribute(t, pool, link, policy)
		if paid > 0 || missing {
			invoice := uuid.NewString()
			if !missing {
				if _, err = pool.Exec(ctx, "INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents,amount_refunded_cents,amount_refund_pending_cents) VALUES ($1,$2,'paid',$3,$4,$4,0,$5,$6)", invoice, account, currency, paid, refunded, pending); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = pool.Exec(ctx, "UPDATE referral_attributions SET created_at='2026-11-03T12:00:00Z',first_paid_at='2026-11-05T12:00:00Z',first_paid_invoice_id=$2 WHERE account_id=$1", account, invoice); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err = pool.Exec(ctx, "UPDATE referral_attributions SET created_at='2026-11-03T12:00:00Z' WHERE account_id=$1", account); err != nil {
				t.Fatal(err)
			}
		}
		return account
	}
	privateAccount := add(sale, "usd", 5000, 500, 200, false)
	add(sale, "eur", 3000, 3000, 0, false)
	add(sale, "usd", 0, 0, 0, false)
	add(sale, "usd", 0, 0, 0, true)
	add(otherSale, "eur", 2000, 0, 0, false)
	add(customer, "usd", 99999, 0, 0, false)
	f := referrals.ReportFilter{From: "2026-11-03", To: "2026-11-03", Timezone: "UTC", MemberEmails: []string{"rep@example.com", "new@example.com"}}
	r := reportFor(t, store, owner, f)
	a, b, n := reportRow(t, r, owner), reportRow(t, r, other), reportRow(t, r, newcomer)
	if len(r.Rows) != 3 || a.Leads.Total != 2 || a.Leads.Won != 1 || a.Signups != 4 || a.FirstPayments != 3 || a.MissingInvoices != 1 || a.AvgFirstPaymentSeconds == nil || *a.AvgFirstPaymentSeconds != 172800 {
		t.Fatal("wrong owner metrics", r)
	}
	if b.CurrentMember || b.Leads.Total != 1 || b.Signups != 1 || b.FirstPayments != 1 {
		t.Fatal("past source lost history", b)
	}
	if !n.CurrentMember || n.Leads.Total != 0 || n.Signups != 0 || n.AvgFirstPaymentSeconds != nil {
		t.Fatal("new rep did not retain empty metrics", n)
	}
	if r.ObservedAt.IsZero() {
		t.Fatal("missing report observation")
	}
	if len(r.Collections) != 3 {
		t.Fatal("collections grouped or multiplied incorrectly", r.Collections)
	}
	for _, cash := range r.Collections {
		if cash.OwnerID == owner && cash.Currency == "usd" && (cash.PaidCents != 5000 || cash.RefundedCents != 500 || cash.PendingRefundCents != 200 || cash.RetainedCents != 4300) {
			t.Fatal(cash)
		}
		if cash.OwnerID == owner && cash.Currency == "eur" && (cash.PaidCents != 3000 || cash.RetainedCents != 0) {
			t.Fatal("refunded payment was lost or mixed with USD", cash)
		}
	}
	raw, err := json.Marshal(r)
	if err != nil || strings.Contains(string(raw), privateAccount) || !strings.Contains(string(raw), `"retained_cents":"4300"`) {
		t.Fatal("report leaked an account or lost exact cents", string(raw), err)
	}
	won.OwnerID = other
	won.Version = 1
	if _, err = store.SaveProspect(ctx, scope, owner, wonID, won); err != nil {
		t.Fatal(err)
	}
	moved := reportFor(t, store, owner, f)
	a, b = reportRow(t, moved, owner), reportRow(t, moved, other)
	if a.Leads.Won != 0 || b.Leads.Won != 1 || a.Signups != 4 || a.FirstPayments != 3 {
		t.Fatal("assignment changed acquisition credit", a, b)
	}
	f.Owner = other
	filtered := reportFor(t, store, owner, f)
	if len(filtered.Rows) != 1 || filtered.Rows[0].ID != other || len(filtered.Reps) != 3 || len(filtered.Collections) != 1 || filtered.Collections[0].OwnerID != other {
		t.Fatal("owner filter mixed scopes", filtered)
	}
	f.Owner = uuid.NewString()
	empty := reportFor(t, store, owner, f)
	if len(empty.Rows) != 0 || len(empty.Collections) != 0 {
		t.Fatal(empty)
	}
}

func TestManagerReportUsesLocalCalendarDates(t *testing.T) {
	cases := []struct {
		name, zone, day string
		at              []string
		want            int64
	}{
		{"istanbul", "Europe/Istanbul", "2026-11-03", []string{"2026-11-02T20:59:59Z", "2026-11-02T21:00:00Z", "2026-11-03T20:59:59Z", "2026-11-03T21:00:00Z"}, 2},
		{"dst_25_hour_day", "America/New_York", "2026-11-01", []string{"2026-11-01T03:59:59Z", "2026-11-01T04:00:00Z", "2026-11-02T04:59:59Z", "2026-11-02T05:00:00Z"}, 2},
		{"missing_midnight", "America/Sao_Paulo", "2018-11-04", []string{"2018-11-04T02:59:59Z", "2018-11-04T03:00:00Z", "2018-11-05T01:59:59Z", "2018-11-05T02:00:00Z"}, 2},
		{"date_repeats", "Antarctica/Casey", "2010-03-05", []string{"2010-03-04T13:00:00Z", "2010-03-04T14:59:59Z", "2010-03-04T15:30:00Z", "2010-03-04T16:00:00Z", "2010-03-05T15:59:59Z", "2010-03-05T16:00:00Z"}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, store, owner, _ := pipelineFixture(t)
			ctx := t.Context()
			link, err := store.EnsureLink(ctx, owner, "sales")
			if err != nil {
				t.Fatal(err)
			}
			for i, at := range tc.at {
				id, err := store.SaveProspect(ctx, referrals.Scope{OwnerID: owner}, owner, "", prospect(owner, fmt.Sprintf("lead%d@example.com", i)))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, "UPDATE sales_leads SET created_at=$2::text::timestamptz WHERE id=$1", id, at); err != nil {
					t.Fatal(err)
				}
				account := attribute(t, pool, link, referrals.Policy{Currency: "usd", ConversionDays: 90})
				if _, err = pool.Exec(ctx, "UPDATE referral_attributions SET created_at=$2::text::timestamptz WHERE account_id=$1", account, at); err != nil {
					t.Fatal(err)
				}
			}
			r := reportFor(t, store, owner, referrals.ReportFilter{From: tc.day, To: tc.day, Timezone: tc.zone})
			row := reportRow(t, r, owner)
			if row.Leads.Total != tc.want || row.Signups != tc.want {
				t.Fatal("date boundary miscount", row, tc)
			}
		})
	}
}

func TestManagerReportRejectsRepScopeBeforeDatabaseAccess(t *testing.T) {
	_, err := referrals.NewPostgresStore(nil).ManagerReport(t.Context(), referrals.Scope{OwnerID: uuid.NewString()}, referrals.ReportFilter{})
	if !errors.Is(err, referrals.ErrReportAccess) {
		t.Fatal(err)
	}
}
