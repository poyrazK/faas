package main

import (
	"fmt"
	"strings"
)

func renderTestActionComparisonMarkdown(out *strings.Builder, comparison testReportComparison) {
	if comparison.ActionStatus == "" {
		return
	}
	fmt.Fprintf(out, "## %s: %s\n\nPlatform-captured evidence sealed before cleanup. Coverage is limited to the declared scope and supported adapters.\n\n", testReportActionLabel(comparison.Runs), strings.ToUpper(comparison.ActionStatus))
	for index, run := range comparison.Runs {
		if index == testComparisonMarkdownRowLimit {
			break
		}
		if run.Actions == nil {
			continue
		}
		actions := run.Actions
		fmt.Fprintf(out, "**%s:** %s · %s → %s %s\n\n", markdownComparisonText(run.Key), markdownComparisonText(actions.Status), testActionCountText(actions.BeforeCount), testActionCountText(actions.AfterCount), testActionUnits(actions))
		if actions.Reason != "" {
			fmt.Fprintf(out, "%s\n\n", markdownComparisonText(actions.Reason))
		}
		if len(actions.Changes) == 0 {
			continue
		}
		out.WriteString("| Workload / source | Effect | Result | Earlier evidence | Later evidence |\n|---|---|---|---|---|\n")
		for i, change := range actions.Changes {
			if i == testComparisonMarkdownRowLimit {
				break
			}
			label := change.Workload + " / " + change.Source
			if change.Workflow != "" {
				label += " / " + change.Workflow + " / " + change.Step
			}
			fmt.Fprintf(out, "| %s | %s | %s | %s | %s |\n", markdownComparisonText(label), markdownComparisonText(change.Name), markdownComparisonText(change.Status), markdownComparisonText(formatTestActionValues(change.Before)), markdownComparisonText(formatTestActionValues(change.After)))
		}
		if len(actions.Changes) > testComparisonMarkdownRowLimit {
			fmt.Fprintf(out, "\nShowing the first %d action groups; the full inventory remains in JSON and HTML.\n", testComparisonMarkdownRowLimit)
		}
		out.WriteString("\n")
	}
}
