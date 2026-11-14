// adr: 20261112223000

package main

import (
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
)

// withFeatureHints fills a plan's feature hints when the server that scanned
// it predates them, so every plan the CLI shows or emits carries them.
func withFeatureHints(plan api.PlanResponse) api.PlanResponse {
	if len(plan.FeatureHints) == 0 {
		plan.FeatureHints = featurehints.For(featurehints.FromPlan(plan))
	}
	return plan
}

// printFeatureHints renders "For this repo": what the plan will do with this
// source and the commands that change it (ADR-20261112223000 §1).
//
//nolint:errcheck // best-effort terminal rendering mirrors printPlanTextWithExplain.
func printFeatureHints(w io.Writer, hints []api.FeatureHint) {
	if len(hints) == 0 {
		return
	}
	shown := 0
	for _, hint := range hints {
		// The plan's "Needs a value" section already lists unset keys.
		if hint.ID == featurehints.IDUnsetEnv {
			continue
		}
		if shown == 0 {
			fmt.Fprintln(w, "\nFor this repo (what Gregale will do, and how to change it):")
		}
		shown++
		fmt.Fprintf(w, "  - %s\n", hint.Applied)
		if hint.Change != "" {
			fmt.Fprintf(w, "    %s\n", hint.Change)
		}
		for _, command := range hint.Commands {
			fmt.Fprintf(w, "      $ %s\n", command)
		}
	}
}
