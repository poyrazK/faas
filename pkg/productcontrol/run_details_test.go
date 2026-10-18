package productcontrol

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func runDetailsFixture() api.ProductRunDetails {
	at := time.Now().Add(-time.Hour).UTC()
	resolved := at.Add(time.Minute)
	amount := int64(25000)
	net := amount
	return api.ProductRunDetails{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), OperationID: uuid.NewString(), Feature: "export", Plan: "starter", Period: Period(at), AdmittedAt: at, ObservedAt: time.Now().UTC(), StartsConsumed: 1, Allowance: &api.ProductRunAllowance{MonthlyStarts: 10, StartsBefore: 3, StartsAfter: 4}, Outcome: api.ProductRunOutcome{State: "succeeded", ResolvedAt: &resolved}, Charge: &api.ProductRunCharge{State: "provisional", Currency: "EUR", UnitPriceMillicents: amount, AmountMillicents: &amount, NetAmountMillicents: &net}}
}

func TestProductControlRunDetailsValidation(t *testing.T) {
	p := api.ProductPermissions{ViewWork: true, ViewBilling: true}
	d := runDetailsFixture()
	if err := ValidateRunDetails(d, p); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ProductRunDetails, *api.ProductPermissions)
	}{
		{"work permission", func(_ *api.ProductRunDetails, p *api.ProductPermissions) { p.ViewWork = false }},
		{"billing permission", func(_ *api.ProductRunDetails, p *api.ProductPermissions) { p.ViewBilling = false }},
		{"missing charge", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Charge = nil }},
		{"foreign ID", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.TenantID = "bad" }},
		{"replenished start", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.StartsConsumed = 0 }},
		{"allowance arithmetic", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Allowance.StartsAfter = 9 }},
		{"over allowance", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Allowance.StartsBefore = 10
			d.Allowance.StartsAfter = 11
		}},
		{"invented outcome", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Outcome.ResolvedAt = nil }},
		{"expired result", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Outcome.ResultAvailable = true }},
		{"pending asserts zero", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Outcome = api.ProductRunOutcome{State: "accepted", DetailsAvailable: true}
			d.Charge.State = "pending"
		}},
		{"pending invented from expiry", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Outcome = api.ProductRunOutcome{State: "accepted"}
			d.Charge.State = "pending"
			d.Charge.AmountMillicents = nil
			d.Charge.NetAmountMillicents = nil
		}},
		{"unresolved asserts zero", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Outcome = api.ProductRunOutcome{State: "unavailable"}
			d.Charge.State = "unresolved"
		}},
		{"negative price", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Charge.UnitPriceMillicents = -1 }},
		{"wrong charge", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { *d.Charge.AmountMillicents = 1 }},
		{"wrong net", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { *d.Charge.NetAmountMillicents = 1 }},
		{"unsigned credit", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Charge.CreditMillicents = 1000 }},
		{"over credit", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Charge.CreditMillicents = -26000 }},
		{"credit without statement", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Charge.State = "partially_credited"
			d.Charge.CreditCount = 1
			d.Charge.CreditMillicents = -1000
			*d.Charge.NetAmountMillicents = 24000
		}},
		{"finalized without statement", func(d *api.ProductRunDetails, _ *api.ProductPermissions) { d.Charge.State = "finalized" }},
		{"success free without waiver", func(d *api.ProductRunDetails, _ *api.ProductPermissions) {
			d.Charge.State = "not_billable"
			*d.Charge.AmountMillicents = 0
			*d.Charge.NetAmountMillicents = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var v api.ProductRunDetails
			if err = json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			permissions := p
			tc.change(&v, &permissions)
			if err = ValidateRunDetails(v, permissions); err == nil {
				t.Fatal("unsafe run details accepted")
			}
		})
	}
	d.Charge = nil
	d.Allowance = nil
	if err := ValidateRunDetails(d, api.ProductPermissions{ViewWork: true}); err != nil {
		t.Fatal("legacy viewer outcome rejected", err)
	}
}
