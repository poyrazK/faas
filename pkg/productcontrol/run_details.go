package productcontrol

import (
	"errors"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateRunDetails validates shape and permissions at the service boundary.
// State stores additionally bind the receipt to the live workspace authority.
func ValidateRunDetails(d api.ProductRunDetails, p api.ProductPermissions) error {
	account, accountErr := uuid.Parse(d.AccountID)
	app, appErr := uuid.Parse(d.AppID)
	if !p.ViewWork || accountErr != nil || appErr != nil || account == uuid.Nil || app == uuid.Nil || !activityUUID(d.TenantID) || !activityUUID(d.OperationID) || !ValidProductPlanName(d.Feature) || !ValidProductPlanName(d.Plan) || ValidatePeriod(d.Period) != nil || d.AdmittedAt.IsZero() || d.ObservedAt.IsZero() || d.AdmittedAt.After(d.ObservedAt) || d.StartsConsumed != 1 {
		return errors.New("invalid customer run scope or admission")
	}
	if a := d.Allowance; a != nil && (a.MonthlyStarts < 1 || a.MonthlyStarts > api.ProductControlMaxMonthlyStarts || a.StartsBefore < 0 || a.StartsBefore >= a.MonthlyStarts || a.StartsAfter != a.StartsBefore+1) {
		return errors.New("invalid customer run allowance")
	}
	o := d.Outcome
	switch o.State {
	case "accepted", "running", "requires_reconciliation":
		if !o.DetailsAvailable || o.ResolvedAt != nil {
			return errors.New("live run lacks retained evidence")
		}
	case "succeeded", "failed", "cancelled":
		if o.ResolvedAt == nil || o.ResolvedAt.IsZero() || o.ResolvedAt.Before(d.AdmittedAt) || o.ResolvedAt.After(d.ObservedAt) {
			return errors.New("resolved run lacks outcome evidence")
		}
	case "unavailable":
		if o.DetailsAvailable || o.ResolvedAt != nil {
			return errors.New("unavailable run contains outcome evidence")
		}
	default:
		return errors.New("invalid customer run outcome")
	}
	if o.ResultAvailable && (!o.DetailsAvailable || o.State != "succeeded") || !p.ViewBilling && d.Charge != nil || p.ViewBilling && d.Charge == nil {
		return errors.New("customer run visibility mismatch")
	}
	if d.Charge != nil {
		return validateRunCharge(d)
	}
	return nil
}

func validateRunCharge(d api.ProductRunDetails) error {
	c := d.Charge
	if !currency.MatchString(c.Currency) || c.UnitPriceMillicents < 0 || c.UnitPriceMillicents > api.ProductControlMaxUnitPriceMillicents || c.CreditCount < 0 || c.CreditMillicents > 0 || c.CreditMillicents < -c.UnitPriceMillicents || (c.CreditCount == 0) != (c.CreditMillicents == 0) {
		return errors.New("invalid customer charge terms")
	}
	if c.Statement != nil && (!activityUUID(c.Statement.ID) || c.Statement.CreatedAt.IsZero() || c.Statement.CreatedAt.Before(d.AdmittedAt) || c.Statement.CreatedAt.After(d.ObservedAt)) {
		return errors.New("invalid customer statement reference")
	}
	known, amount := true, int64(0)
	switch c.State {
	case "pending":
		known = false
		if d.Outcome.State != "accepted" && d.Outcome.State != "running" {
			return errors.New("pending charge lacks live work")
		}
	case "awaiting_review":
		known = false
		if d.Outcome.State != "requires_reconciliation" {
			return errors.New("charge review lacks outcome evidence")
		}
	case "unresolved":
		known = false
		if d.Outcome.State != "unavailable" {
			return errors.New("unresolved charge has an outcome")
		}
	case "not_billable":
		if d.Outcome.State != "failed" && d.Outcome.State != "cancelled" {
			return errors.New("nonbillable charge lacks outcome evidence")
		}
	case "waived":
	case "provisional", "finalized", "partially_credited", "credited":
		amount = c.UnitPriceMillicents
		if d.Outcome.State != "succeeded" || (c.State == "provisional") != (c.Statement == nil) {
			return errors.New("success charge lacks outcome or statement evidence")
		}
	default:
		return errors.New("invalid customer charge state")
	}
	if !known {
		if c.AmountMillicents != nil || c.NetAmountMillicents != nil || c.Statement != nil || c.CreditCount != 0 {
			return errors.New("unknown charge asserts an amount")
		}
		return nil
	}
	if c.AmountMillicents == nil || c.NetAmountMillicents == nil || *c.AmountMillicents != amount || *c.NetAmountMillicents != amount+c.CreditMillicents {
		return errors.New("customer charge amounts disagree")
	}
	credited := c.State == "partially_credited" || c.State == "credited"
	if credited != (c.CreditCount > 0) || credited && (c.Statement == nil || amount == 0 || (c.State == "credited") != (*c.NetAmountMillicents == 0)) {
		return errors.New("customer credits disagree")
	}
	return nil
}
