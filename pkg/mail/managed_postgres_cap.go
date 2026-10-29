package mail

import (
	"fmt"
	"time"
)

// Managed PostgreSQL compute cap thresholds (ADR-20261025094915).
const (
	ManagedPostgresCapWarning        = "warning"
	ManagedPostgresCapAllowance      = "allowance"
	ManagedPostgresCapCeilingWarning = "ceiling_warning"
	ManagedPostgresCapSuspended      = "suspended"
)

// ManagedPostgresCapNotice is the frozen snapshot one cap email reports.
type ManagedPostgresCapNotice struct {
	Threshold   string
	UsedSeconds int64
	Allowance   int64
	Ceiling     int64
	Overage     bool
	ReachedAt   time.Time
}

// ManagedPostgresCapBodyV1 renders one compute cap email. Amounts are
// compute-unit hours; nothing here prices or bills them.
func ManagedPostgresCapBodyV1(email string, n ManagedPostgresCapNotice) (subject, body string, ok bool) {
	used, allowance, ceiling := cuHours(n.UsedSeconds), cuHours(n.Allowance), cuHours(n.Ceiling)
	resume := time.Date(n.ReachedAt.UTC().Year(), n.ReachedAt.UTC().Month()+1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var detail string
	switch n.Threshold {
	case ManagedPostgresCapWarning:
		subject = "Your managed PostgreSQL databases have used 80% of this month's compute"
		next := fmt.Sprintf("When the allowance is used, the databases are suspended until %s, unless you upgrade your plan or turn on compute overage.", resume)
		if n.Overage {
			next = fmt.Sprintf("Compute overage is on, so past the allowance the databases keep running until %s, and are then suspended until %s.", ceiling, resume)
		}
		detail = fmt.Sprintf("They have used %s of this month's %s allowance.\n%s", used, allowance, next)
	case ManagedPostgresCapAllowance:
		if !n.Overage {
			return "", "", false
		}
		subject = "Your managed PostgreSQL databases are past this month's compute allowance"
		detail = fmt.Sprintf("They have used %s, past the %s allowance. Compute overage is on, so they keep running until %s, and are then suspended until %s.\nCompute above the allowance is not billed yet.", used, allowance, ceiling, resume)
	case ManagedPostgresCapCeilingWarning:
		subject = "Your managed PostgreSQL databases are close to this month's compute limit"
		detail = fmt.Sprintf("They have used %s of the %s limit. At the limit they are suspended until %s, unless you upgrade your plan.", used, ceiling, resume)
	case ManagedPostgresCapSuspended:
		subject = "Your managed PostgreSQL databases are suspended until next month"
		next := "Upgrade your plan or turn on compute overage to resume them sooner."
		if n.Overage {
			next = "Upgrade your plan to resume them sooner."
		}
		detail = fmt.Sprintf("They used this month's %s compute limit at %s, so they were suspended.\nTheir data, backups and connection settings are kept. They resume on %s.\n%s", ceiling, n.ReachedAt.UTC().Format("2006-01-02 15:04 UTC"), resume, next)
	default:
		return "", "", false
	}
	body = fmt.Sprintf(`Hi,

This is about the managed PostgreSQL databases of your Gregale account (%s).
%s

See usage with gregale postgres usage or on the PostgreSQL page of the dashboard.

— Gregale
`, safeRecipient(email), detail)
	return subject, body, true
}

func cuHours(seconds int64) string {
	return fmt.Sprintf("%.1f compute-unit hours", float64(seconds)/3600)
}
