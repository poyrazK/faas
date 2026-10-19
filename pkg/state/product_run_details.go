package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type ProductRunDetailsScope struct{ ProductPlanScope }

type ProductRunDetailsStore interface {
	GetProductRunDetails(context.Context, ProductRunDetailsScope, string) (api.ProductRunDetails, error)
}

func productRunDetails(s ProductRunDetailsScope, id string, r api.ProductOperationReceipt, resolved api.OperationState, resolvedAt *time.Time, live string, result bool, waived bool, p api.ProductPermissions, now time.Time) api.ProductRunDetails {
	d := api.ProductRunDetails{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, OperationID: id, Feature: r.Feature, Plan: r.Plan, Period: r.Period, AdmittedAt: r.AdmittedAt.UTC(), ObservedAt: now.UTC(), StartsConsumed: 1, Outcome: productRunOutcome(resolved, resolvedAt, live, result)}
	if a := r.Admission; a != nil {
		d.Allowance = &api.ProductRunAllowance{MonthlyStarts: a.MonthlyStarts, StartsBefore: a.StartsBefore, StartsAfter: a.StartsBefore + 1}
	}
	if p.ViewBilling {
		c := &api.ProductRunCharge{Currency: r.Currency, UnitPriceMillicents: r.UnitPriceMillicents, State: "unresolved"}
		amount := int64(0)
		switch {
		case waived:
			c.State = "waived"
		case resolved == api.OperationFailed || resolved == api.OperationCancelled:
			c.State = "not_billable"
		case resolved == api.OperationSucceeded:
			c.State, amount = "provisional", r.UnitPriceMillicents
		case live == "requires_reconciliation":
			c.State = "awaiting_review"
		case live == "accepted" || live == "running":
			c.State = "pending"
		}
		if c.State == "waived" || c.State == "not_billable" || c.State == "provisional" {
			net := amount
			c.AmountMillicents, c.NetAmountMillicents = &amount, &net
		}
		d.Charge = c
	}
	return d
}

func productRunOutcome(resolved api.OperationState, resolvedAt *time.Time, live string, result bool) api.ProductRunOutcome {
	o := api.ProductRunOutcome{State: "unavailable", DetailsAvailable: live != ""}
	if resolved != "" {
		o.State = string(resolved)
		if resolvedAt != nil {
			at := resolvedAt.UTC()
			o.ResolvedAt = &at
		}
	} else if live != "" {
		o.State = live
	}
	o.ResultAvailable = result && o.DetailsAvailable && o.State == "succeeded"
	return o
}

func productRunFinalCharge(d *api.ProductRunDetails) error {
	c := d.Charge
	if c != nil && c.AmountMillicents != nil {
		net := *c.AmountMillicents + c.CreditMillicents
		c.NetAmountMillicents = &net
		if c.State == "provisional" && c.Statement != nil {
			c.State = "finalized"
		}
		if c.CreditCount > 0 {
			c.State = "partially_credited"
			if net == 0 {
				c.State = "credited"
			}
		}
	}
	if productcontrol.ValidateRunDetails(*d, api.ProductPermissions{ViewWork: true, ViewBilling: c != nil}) != nil {
		return ErrConflict
	}
	return nil
}
