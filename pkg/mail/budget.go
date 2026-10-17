package mail

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

// BudgetThreshold describes one ADR-1500 budget notification.
type BudgetThreshold struct {
	BudgetName          string
	Basis               string // net_usage or gross_usage
	ThresholdMillicents int64
	KnownMillicents     int64
	LimitMillicents     int64
	PeriodStart         time.Time
	CoverageComplete    bool
}

// BudgetThresholdBody renders the "your budget passed $X" email. Spending
// is the known lower bound from retained evidence; when coverage is
// incomplete the body says the real amount may be higher.
func BudgetThresholdBody(b BudgetThreshold) (subject, body string) {
	name := safeBudgetName(b.BudgetName)
	threshold := api.FormatBillingMillicents(b.ThresholdMillicents)
	subject = fmt.Sprintf("Gregale budget %q passed %s this month", name, threshold)
	basis := "net"
	if b.Basis == "gross_usage" {
		basis = "gross"
	}
	coverage := ""
	if !b.CoverageComplete {
		coverage = "\nSome of this month's usage is not recorded yet, so the real amount\nmay be higher.\n"
	}
	body = fmt.Sprintf(`Hi,

Known %s usage covered by your budget %q has passed %s for the
month that started %s (UTC).

  Known spending so far: %s
  Budget limit:          %s
%s
This budget only notifies you. Your apps and jobs keep running.

To see where the spending comes from, run:

    gregale billing costs

To change or turn off this budget, run:

    gregale billing budgets list

You get one email per threshold each month.
`, basis, name, threshold, b.PeriodStart.UTC().Format("2006-01-02"),
		api.FormatBillingMillicents(b.KnownMillicents), api.FormatBillingMillicents(b.LimitMillicents), coverage)
	return subject, body
}

// safeBudgetName removes control characters (CR/LF would inject headers into
// the subject) and bounds the length of a customer-chosen budget name.
func safeBudgetName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > api.FinancialBudgetNameBytes {
		name = strings.ToValidUTF8(name[:api.FinancialBudgetNameBytes], "")
	}
	if name == "" {
		name = "budget"
	}
	return name
}
