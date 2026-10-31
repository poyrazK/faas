package mail

import (
	"fmt"
	"time"
)

// SpendCapReachedBody tells a paid account that its monthly spend cap stopped
// its workloads (ADR-20261030093512). meterd sends it once when the cap's hold
// first engages in a month; without it a customer on the default cap saw apps
// stop with nothing to explain why.
func SpendCapReachedBody(email, plan string, capCents, observedCents int64, at time.Time) (subject, body string) {
	email = safeRecipient(email)
	month := at.UTC().Format("January 2006")
	subject = "Your Gregale apps are paused: monthly spend cap reached"
	body = fmt.Sprintf(`Hi,

Your Gregale account (%s) on the %s plan reached its monthly spend cap for
%s on %s.

  Spend cap:           %s
  Overage this month:  %s

While the cap holds, Gregale parks running instances and refuses new wakes,
so overage stops growing. Usage already recorded is billed as usual. Apps
start again on the 1st of next month, or as soon as you raise or remove the
cap:

    gregale billing spend-cap <cents>
    gregale billing spend-cap --clear

or change it on the billing page in the dashboard.

— Gregale
`, email, plan, month, at.UTC().Format("2006-01-02 15:04 UTC"), formatUSDCents(capCents), formatUSDCents(observedCents))
	return subject, body
}

func formatUSDCents(cents int64) string {
	if cents < 0 {
		return "-" + formatUSDCents(-cents)
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
