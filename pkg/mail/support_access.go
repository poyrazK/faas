package mail

import (
	"fmt"
	"strings"
	"time"
)

// Customer-granted support access (ADR-20261116163508): when a grant ends,
// its organization's owners and admins get one mail that lists what support
// did under it.

// SupportAccessAction is one thing support did under a grant.
type SupportAccessAction struct {
	At       time.Time
	Operator string
	App      string
	What     string
	Status   int
}

// SupportAccessSummary is a grant that ended and what was done under it.
type SupportAccessSummary struct {
	OrgName   string
	Apps      string
	GrantedBy string
	Started   time.Time
	Ended     time.Time
	// EndedBy is who revoked the grant; empty when it expired.
	EndedBy string
	Actions []SupportAccessAction
	// More counts actions beyond those listed.
	More int
	// OrgSlug names the organization in the CLI hint.
	OrgSlug string
	// PageURL is the dashboard's Support access page.
	PageURL string
}

// SupportAccessSummaryBody is the end-of-access summary mail.
func SupportAccessSummaryBody(s SupportAccessSummary) (subject, body string) {
	org := oneLine(s.OrgName)
	subject = fmt.Sprintf("Gregale support's access to %s ended", org)
	ended := "It expired"
	if s.EndedBy != "" {
		ended = "It was revoked by " + oneLine(s.EndedBy)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nGregale support's access to %s in the organization %s ended at %s. %s.\n",
		oneLine(s.Apps), org, mailTime(s.Ended), ended)
	fmt.Fprintf(&b, "%s granted it at %s.\n\n", oneLine(s.GrantedBy), mailTime(s.Started))
	if len(s.Actions) == 0 {
		b.WriteString("Support made no changes under this access.\n")
	} else {
		b.WriteString("What support did:\n\n")
		for _, a := range s.Actions {
			result := "done"
			if a.Status >= 400 {
				result = fmt.Sprintf("failed (%d)", a.Status)
			}
			fmt.Fprintf(&b, "- %s  %s on %s by %s: %s\n", mailTime(a.At), oneLine(a.What), oneLine(a.App), oneLine(a.Operator), result)
		}
		if s.More > 0 {
			fmt.Fprintf(&b, "- and %d more\n", s.More)
		}
	}
	fmt.Fprintf(&b, "\nEvery action is in the organization's activity log: gregale org activity --org %s --actor-type operator.\n", oneLine(s.OrgSlug))
	if s.PageURL != "" {
		fmt.Fprintf(&b, "Grants and requests: %s\n", oneLine(s.PageURL))
	}
	b.WriteString("If anything here is unexpected, reply to this mail or write to support@gregale.dev.\n\n— Gregale\n")
	return subject, b.String()
}
