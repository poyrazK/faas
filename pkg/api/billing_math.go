package api

// Billing units are deliberately integer based. Usage is recorded as
// megabyte-seconds, while the financial model prices overage in cents per
// gigabyte-hour. Keeping the conversion here gives meterd, invoice reducers,
// and provider adapters one canonical quota boundary.
const (
	MBPerGB           int64 = 1024
	SecondsPerHour    int64 = 3600
	SecondsPerGBHour  int64 = MBPerGB * SecondsPerHour
	MillicentsPerCent int64 = 1000
)

// BillableMBSeconds returns the portion of total usage above the plan's
// included calendar-month allowance. It is also the quantity Gregale sends to
// providers that meter only overage (Polar), so a plan's included usage is not
// charged twice or reset on a provider subscription anniversary.
func BillableMBSeconds(plan Plan, totalMBSeconds int64) int64 {
	if totalMBSeconds <= 0 {
		return 0
	}
	included := int64(plan.PlanIncludedGBHours()) * SecondsPerGBHour
	if totalMBSeconds <= included {
		return 0
	}
	return totalMBSeconds - included
}

// OverageMillicentsForBillableMBSeconds converts already-net overage usage to
// millicents. The result is floored at the sub-millicent boundary; no floating
// point arithmetic is used near money.
func OverageMillicentsForBillableMBSeconds(billable int64) int64 {
	if billable <= 0 {
		return 0
	}
	return billable * OverageMillicentsPerGBHour / SecondsPerGBHour
}

// OverageMillicentsForMBSeconds converts total usage for one plan and one
// calendar-month period to overage millicents.
func OverageMillicentsForMBSeconds(plan Plan, totalMBSeconds int64) int64 {
	billable := BillableMBSeconds(plan, totalMBSeconds)
	if billable == 0 {
		return 0
	}
	return OverageMillicentsForBillableMBSeconds(billable)
}

// OverageCentsForMBSeconds converts total usage to integer cents. It floors
// fractional cents, matching the invoice and overage-cap contract.
func OverageCentsForMBSeconds(plan Plan, totalMBSeconds int64) int64 {
	return OverageMillicentsForMBSeconds(plan, totalMBSeconds) / MillicentsPerCent
}

// OverageCentsForBillableMBSeconds converts already-net overage usage to
// integer cents. It is used when a range crosses a quota-reset boundary and
// each calendar-month segment has already had its allowance removed.
func OverageCentsForBillableMBSeconds(billable int64) int64 {
	return OverageMillicentsForBillableMBSeconds(billable) / MillicentsPerCent
}
