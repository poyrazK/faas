package mail

import (
	"fmt"
	"strings"
)

// PlanFitApp is one app outside its billing subject's plan, as schedd parks
// it (ADR-20261103131742, billing review H17-4).
type PlanFitApp struct {
	Slug string
	// RAMMB is set when the app's RAM is above the plan's per-app RAM.
	RAMMB int
	// PastAppLimit is set when the app ranks past the plan's deployed-app
	// count (oldest apps are kept).
	PastAppLimit bool
}

// PlanFitBody renders the email sent when a subject's apps stop fitting its
// plan: which apps are parked and what lets them wake again.
func PlanFitBody(email, plan string, deployedAppLimit, ramLimitMB int, apps []PlanFitApp) (subject, body string) {
	var lines []string
	for _, app := range apps {
		switch {
		case app.RAMMB > 0:
			lines = append(lines, fmt.Sprintf("- %s: its RAM (%d MB) is above the %s plan's %d MB. Lower it with gregale app %s --ram %d.",
				app.Slug, app.RAMMB, plan, ramLimitMB, app.Slug, ramLimitMB))
		case app.PastAppLimit:
			lines = append(lines, fmt.Sprintf("- %s: the %s plan allows %d deployed app(s), and your oldest apps are kept.",
				app.Slug, plan, deployedAppLimit))
		}
	}
	if len(apps) == 1 {
		subject = fmt.Sprintf("Your app %s is parked: it does not fit the %s plan", apps[0].Slug, plan)
	} else {
		subject = fmt.Sprintf("%d of your apps are parked: they do not fit the %s plan", len(apps), plan)
	}
	body = fmt.Sprintf(`Hi %s,

Your account is now on the %s plan, and these apps do not fit it:

%s

They are parked and do not wake until they fit. Their code, configuration, domains and data are kept.
Each app wakes again within a minute once it fits: delete apps you no longer need, lower their RAM, or upgrade your plan.

Plans: https://gregale.dev/docs/plans
`, email, plan, strings.Join(lines, "\n"))
	return subject, body
}
