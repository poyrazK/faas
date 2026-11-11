package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// functionLimitsLine is the one line a function deploy prints about how the
// deployed function runs: its memory, request timeout, pause behaviour and
// when it parks. Every value comes from the app as the API resolved it, so
// the line shows what the platform applies, defaults included. Values an
// older API does not report are left out.
func functionLimitsLine(app api.AppResponse) string {
	var parts []string
	if app.RAMMB > 0 {
		parts = append(parts, fmt.Sprintf("%d MB", app.RAMMB))
	}
	timeoutS := int64(app.RequestTimeoutS)
	if timeoutS <= 0 {
		timeoutS = app.EffectiveLimits.RequestBudgetMS / 1000
	}
	if timeoutS > 0 {
		parts = append(parts, humanSeconds(timeoutS)+" timeout")
	}
	switch app.EffectiveLimits.Pause {
	case api.PauseBetweenRequests:
		parts = append(parts, "pauses between requests")
	case api.PauseNever:
		parts = append(parts, "never paused")
	}
	switch {
	case app.MinInstances > 0:
		parts = append(parts, fmt.Sprintf("keeps %d warm", app.MinInstances))
	case app.EffectiveLimits.IdleTimeoutS > 0:
		parts = append(parts, "parks after "+humanSeconds(int64(app.EffectiveLimits.IdleTimeoutS))+" idle")
	}
	return strings.Join(parts, " · ")
}

// humanSeconds renders whole minutes as "5 min" and anything else as "45 s".
func humanSeconds(s int64) string {
	if s >= 60 && s%60 == 0 {
		return fmt.Sprintf("%d min", s/60)
	}
	return fmt.Sprintf("%d s", s)
}

// printFunctionLimits prints the limits line of a deployed function app.
func printFunctionLimits(out io.Writer, app api.AppResponse) {
	if app.Type != "function" {
		return
	}
	if line := functionLimitsLine(app); line != "" {
		_, _ = fmt.Fprintf(out, "  Runs with %s\n", line)
	}
}
