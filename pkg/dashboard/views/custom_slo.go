package views

// CustomSLOView is one row of the app page's "Your SLOs" table (ADR-747).
// Every field is pre-formatted at the handler edge so the template stays a
// pure renderer; BudgetClass picks the badge colour.
type CustomSLOView struct {
	Name            string
	Objective       string // "99.9% available over 30d"
	Attained        string // "99.950%" or "no traffic"
	BudgetRemaining string // "50.0%" or "—"
	BudgetClass     string // "ok" | "warn" | "bad" | "dim"
	BurnRate        string // "0.80× (1h)" or "—"
	History         string // "712 / 720 h"
	Degraded        string // non-empty when a source was unavailable
}
