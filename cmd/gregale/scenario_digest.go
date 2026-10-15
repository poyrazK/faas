package main

import (
	"errors"
	"flag"
	"fmt"
	"html"
	"net/url"
	"os"
	"sort"
	"strings"
	"unicode"
)

func cmdTestDigest(args []string) int {
	fs := newFlagSet("test digest", flag.ContinueOnError)
	comparisonJSON := fs.String("comparison-json", "", "write a versioned JSON baseline comparison (requires --baseline)")
	baseline := fs.String("baseline", "", "optional previous JSON report")
	baselineEvidence := fs.String("baseline-evidence-url", "", "HTTPS URL of the baseline HTML report")
	from := fs.String("from", "", "saved JSON scenario report")
	markdown := fs.String("markdown", "", "write a Markdown CI digest")
	evidence := fs.String("evidence-url", "", "public HTTPS URL of the matching HTML report")
	limit := fs.Int("limit", 5, "maximum failure groups (1..20)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return 1
	}
	if *from == "" || *markdown == "" || *limit < 1 || *limit > 20 {
		return printErr("Invalid digest options", errors.New("--from and --markdown are required; --limit must be 1..20"))
	}
	if *comparisonJSON != "" && *baseline == "" {
		return printErr("Invalid digest options", errors.New("--comparison-json requires --baseline"))
	}
	outputs := []string{*markdown}
	if *comparisonJSON != "" {
		if err := testDigestDistinctOutputs(*markdown, *comparisonJSON); err != nil {
			return printErr("Invalid digest outputs", err)
		}
		outputs = append(outputs, *comparisonJSON)
	}
	origin, err := validateTestDigestURL(*evidence)
	if err != nil {
		return printErr("Invalid evidence URL", err)
	}
	receipts, _, err := readTestBaselineReport(*from, outputs)
	if err != nil {
		return printErr("Could not read scenario report", err)
	}
	var comparisonBody []byte
	if *comparisonJSON != "" {
		previous, _, readErr := readTestBaselineReport(*baseline, outputs)
		if readErr != nil {
			return printErr("Could not read baseline", readErr)
		}
		baselineURL, urlErr := validateTestDigestURL(*baselineEvidence)
		if urlErr != nil {
			return printErr("Invalid baseline evidence URL", urlErr)
		}
		comparisonBody, err = marshalTestDigestComparison(previous, receipts, baselineURL, origin)
		if err != nil {
			return printErr("Could not encode comparison", err)
		}
	}
	body := renderTestDigest(receipts, *limit, origin)
	body, err = appendTestDigestBaseline(body, receipts, *baseline, *baselineEvidence, *markdown, origin, *limit)
	if err != nil {
		return printErr("Could not compare baseline", err)
	}
	if err := os.WriteFile(*markdown, []byte(body), 0o600); err != nil {
		return printErr("Could not write digest", err)
	}
	if *comparisonJSON != "" {
		if err := os.WriteFile(*comparisonJSON, comparisonBody, 0o600); err != nil {
			return printErr("Could not write comparison JSON", err)
		}
	}
	return 0
}

func validateTestDigestURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("evidence URL must be HTTPS without credentials, query or fragment")
	}
	return u.String(), nil
}

type testDigestGroup struct {
	group testFailureGroup
	root  int
}

type testDigestIssue struct {
	root int
	text string
}

func collectTestDigestGroups(receipts []testRunReceipt) []testDigestGroup {
	var rows []testDigestGroup
	for root, r := range receipts {
		groups := r.FailureGroups()
		if r.FaultMatrix == nil && r.Resilience == nil && r.Status == "failed" {
			summary := r.FailureSummary()
			group := testFailureGroup{Version: 1, Key: testFailureGroupingKey(r, summary), Failures: 1, ExecutedRuns: 1,
				Occurrences: []testFailureOccurrence{{RunID: r.RunID, Attempt: r.Attempt, Failure: summary.Failure, Replay: summary.Replay}},
			}
			updateTestFailureWorst(&group, summary.Assertion)
			groups = append(groups, group)
		}
		for _, group := range groups {
			if group.Key.Kind == "unclassified" {
				continue
			}
			rows = append(rows, testDigestGroup{group: group, root: root})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].group.Failures > rows[j].group.Failures })
	return rows
}

func collectTestDigestIssues(r testRunReceipt, root int, cleanup, other *[]testDigestIssue) {
	if r.CleanupError != "" {
		*cleanup = append(*cleanup, testDigestIssue{root: root, text: r.Scenario + " / " + r.RunID + ": " + r.CleanupError})
	}
	if (r.FaultMatrix != nil || r.Resilience != nil) && r.Status == "failed" && r.Error != "" {
		*other = append(*other, testDigestIssue{root: root, text: r.Scenario + ": " + r.Error})
	}
	if r.FaultMatrix == nil && r.Resilience == nil && r.Status == "failed" {
		summary := r.FailureSummary()
		knownError := r.Error != ""
		for _, step := range r.Steps {
			knownError = knownError || step.Status == "failed"
		}
		if summary.Assertion == nil && (knownError || r.CleanupError == "") {
			*other = append(*other, testDigestIssue{root: root, text: r.Scenario+": "+summary.Failure})
		}
	}
	if r.FaultMatrix != nil {
		for _, child := range r.FaultMatrix.Cases {
			collectTestDigestIssues(child, root, cleanup, other)
		}
	} else if r.Resilience != nil {
		for _, child := range r.Resilience.History {
			collectTestDigestIssues(child, root, cleanup, other)
		}
	}
}

func renderTestDigest(receipts []testRunReceipt, limit int, evidence string) string {
	var out strings.Builder
	counts := make(map[string]int)
	var cleanup, other []testDigestIssue
	for i, r := range receipts {
		counts[r.Status]++
		collectTestDigestIssues(r, i, &cleanup, &other)
	}
	_, _ = fmt.Fprintln(&out, "# Gregale test digest")
	_, _ = fmt.Fprintf(&out, "\nTop-level receipts: %d passed, %d failed, %d skipped, %d other.\n",
		counts["passed"], counts["failed"], counts["skipped"], len(receipts)-counts["passed"]-counts["failed"]-counts["skipped"])
	_, _ = fmt.Fprintln(&out, "\nThis is a descriptive report, not a test gate. Frequencies use executed runs in each original context; tolerated failures do not change parent verdicts.")
	rows := collectTestDigestGroups(receipts)
	_, _ = fmt.Fprintf(&out, "\n## Failure groups (%d total)\n", len(rows))
	for i, row := range rows {
		if i >= limit {
			_, _ = fmt.Fprintf(&out, "\n%d additional groups omitted; inspect the full report.\n", len(rows)-i)
			break
		}
		writeTestDigestGroup(&out, row, i+1, evidence)
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(&out, "\nNo grouped assertion failures recorded. Check other recorded failures and cleanup findings below.")
	}
	writeTestDigestIssues(&out, "Other recorded failures", other, evidence)
	writeTestDigestIssues(&out, "Cleanup errors", cleanup, evidence)
	_, _ = fmt.Fprintln(&out, "\nAbsence of cleanup errors does not independently verify retirement. Replace replay path placeholders with the original report and manifest. Summaries are derived from raw receipts.")
	return out.String()
}

func writeTestDigestGroup(out *strings.Builder, row testDigestGroup, number int, evidence string) {
	g := row.group
	_, _ = fmt.Fprintf(out, "\n### %d. %s / %s\n", number, testDigestText(g.Key.Scenario), testDigestText(g.Key.Assertion))
	_, _ = fmt.Fprintf(out, "\n- Context: %s / %s / %s; step %s, phase %s, request %s.\n",
		testDigestText(g.Key.Profile), testDigestText(g.Key.Engine), testDigestText(g.Key.Case),
		testDigestText(g.Key.Step), testDigestText(g.Key.Phase), testDigestText(g.Key.Request))
	_, _ = fmt.Fprintf(out, "- Frequency: %d / %d executed runs. Fault kinds: %s.\n", g.Failures, g.ExecutedRuns, testDigestText(g.Key.FaultKinds))
	_, _ = fmt.Fprintf(out, "- Worst observed: %s; associated limit: %s (%s).\n",
		testPhaseComparisonValue(g.WorstObserved), testPhaseComparisonValue(g.WorstLimit), testDigestText(g.Operator))
	_, _ = fmt.Fprintf(out, "- %s\n", testDigestEvidenceLink(evidence, row.root))
	for i, occurrence := range g.Occurrences {
		if i == 3 {
			_, _ = fmt.Fprintf(out, "- %d additional affected runs omitted.\n", len(g.Occurrences)-i)
			break
		}
		_, _ = fmt.Fprintf(out, "- Run %s (attempt %d): %s\n", testDigestText(occurrence.RunID), occurrence.Attempt, testDigestText(occurrence.Failure))
		if occurrence.Replay != "" {
			if len(occurrence.Replay) <= 4096 && strings.IndexFunc(occurrence.Replay, unicode.IsControl) < 0 {
				_, _ = fmt.Fprintf(out, "  - Replay: <code>%s</code>\n", testDigestReplayText(occurrence.Replay))
			} else {
				_, _ = fmt.Fprintln(out, "  - Replay omitted: selector exceeds display limits or contains control characters.")
			}
		}
	}
}

func writeTestDigestIssues(out *strings.Builder, title string, issues []testDigestIssue, evidence string) {
	_, _ = fmt.Fprintf(out, "\n## %s (%d)\n", title, len(issues))
	for i, issue := range issues {
		if i == 10 {
			_, _ = fmt.Fprintf(out, "\n%d additional findings omitted; inspect the full report.\n", len(issues)-i)
			break
		}
		_, _ = fmt.Fprintf(out, "\n- %s — %s\n", testDigestText(issue.text), testDigestEvidenceLink(evidence, issue.root))
	}
	if len(issues) == 0 {
		_, _ = fmt.Fprintln(out, "\nNone recorded.")
	}
}

func testDigestEvidenceLink(evidence string, root int) string {
	if evidence == "" {
		return fmt.Sprintf("Evidence: top-level receipt %d in the original JSON/HTML report.", root+1)
	}
	return fmt.Sprintf("[Evidence](<%s#run-evidence-%d>)", evidence, root)
}

func testDigestText(value string) string {
	if value == "" {
		return "unavailable"
	}
	runes := []rune(value)
	if len(runes) > 256 {
		value = string(runes[:256]) + "…"
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = html.EscapeString(value)
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "&#96;", "!", "\\!").Replace(value)
}

func testDigestReplayText(command string) string {
	var out strings.Builder
	// Encode punctuation as entities so Markdown cannot interpret links or
	// emphasis inside the inline HTML code element. Rendered text stays exact.
	for _, r := range command {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			_, _ = fmt.Fprintf(&out, "&#%d;", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
