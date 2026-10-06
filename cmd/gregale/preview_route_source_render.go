package main

import (
	"fmt"
	"io"
	"strings"
)

func previewSourceDisplay(value string, markdown bool) string {
	value = previewReportText(value)
	if markdown {
		value = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "`", "\\`", "|", "\\|", "#", "\\#").Replace(value)
	}
	return value
}

func renderPreviewSourceReview(w io.Writer, report previewRouteReport, markdown bool) {
	if report.SourceImpact == nil {
		return
	}
	source := report.SourceImpact
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Source impact and review priorities\n\n")
	} else {
		_, _ = fmt.Fprint(w, "\nSource impact and review priorities\n")
	}
	_, _ = fmt.Fprintf(w, "Source: %s; analysis: %s; route mapping: %s; unresolved analysis issues: %d\n\n", source.Status, source.AnalysisStatus, source.MappingStatus, source.IssueCount)
	_, _ = fmt.Fprintf(w, "Analyzed source: %s; root %s\nAnalyzed revisions: baseline %s; candidate %s\n\n", previewSourceDisplay(source.Repository, markdown), previewSourceDisplay(source.SourceRoot, markdown), source.BaseRevision, source.CandidateRevision)
	for _, binding := range []struct {
		name string
		data previewSourceBinding
	}{{"Baseline", source.Base}, {"Candidate", source.Candidate}} {
		_, _ = fmt.Fprintf(w, "%s provenance: %s", binding.name, binding.data.Status)
		if binding.data.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", binding.data.Reason)
		}
		_, _ = fmt.Fprintf(w, "; %s @ %s; root %s\n", previewSourceDisplay(binding.data.Repository, markdown), binding.data.Commit, previewSourceDisplay(binding.data.SourceRoot, markdown))
	}
	_, _ = fmt.Fprintf(w, "\n%s\n\n", source.Scope)
	renderPreviewReviewQueue(w, report, markdown)
	for _, route := range report.Routes {
		if route.SourceImpact != nil {
			renderPreviewSourceReferences(w, previewReportRouteKey(route.Method, route.Path), *route.SourceImpact, markdown)
		}
	}
	if len(source.Unmatched) > 0 {
		_, _ = fmt.Fprint(w, "\nUnmatched source findings (no deployment tests or traffic attached):\n\n")
	}
	for _, unmatched := range source.Unmatched {
		label := previewReportRouteKey(unmatched.Method, unmatched.Path)
		_, _ = fmt.Fprintf(w, "- %s: %s\n", previewSourceDisplay(label, markdown), unmatched.Reason)
		renderPreviewSourceReferences(w, label, unmatched.Source, markdown)
	}
}

func renderPreviewSourceReferences(w io.Writer, label string, source previewRouteSource, markdown bool) {
	_, _ = fmt.Fprintf(w, "\n%s: %s (%s; match %s; %d uncertainties)\n", previewSourceDisplay(label, markdown), source.Change, source.Precision, source.Match, source.UncertaintyCount)
	if source.Before != nil {
		_, _ = fmt.Fprintf(w, "- before handler: %s:%d\n", previewSourceDisplay(source.Before.File, markdown), source.Before.Line)
	}
	if source.After != nil {
		_, _ = fmt.Fprintf(w, "- after handler: %s:%d\n", previewSourceDisplay(source.After.File, markdown), source.After.Line)
	}
	for _, evidence := range source.Evidence {
		chain := []string{}
		for _, location := range evidence.ViaSymbols {
			chain = append(chain, fmt.Sprintf("%s (%s:%d)", location.Name, location.File, location.Line))
		}
		if len(chain) == 0 {
			chain = evidence.Via
		}
		_, _ = fmt.Fprintf(w, "- %s %s: %s:%d; %s\n", evidence.Revision, evidence.Kind, previewSourceDisplay(evidence.File, markdown), evidence.Line, previewSourceDisplay(strings.Join(chain, " -> "), markdown))
	}
}
