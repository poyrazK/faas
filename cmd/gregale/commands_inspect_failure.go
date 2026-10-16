package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Human-only projection: retain no log excerpts, input, or deployment payload.
type inspectFailureOverview struct {
	DeploymentID string
	Candidate    bool
	Cause        string
	Recovery     string
}

func inspectPersistedFailure(deployment, candidate *api.DeploymentResponse) *inspectFailureOverview {
	failed := deployment
	isCandidate := false
	if candidate != nil {
		failed, isCandidate = candidate, true
	}
	if failed == nil || failed.Status != "failed" {
		return nil
	}
	cause := firstInspectFailureText(failed.ErrorWhy, failed.Error, failed.ErrorHint)
	recovery := firstInspectFailureText(failed.ErrorFix, failed.ErrorHint)
	if cause == "" && recovery == "" {
		return nil
	}
	if cause == recovery {
		recovery = ""
	}
	return &inspectFailureOverview{DeploymentID: failed.ID, Candidate: isCandidate, Cause: cause, Recovery: recovery}
}

func firstInspectFailureText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return compactInspectFailureText(value)
		}
	}
	return ""
}

func compactInspectFailureText(value string) string {
	value = oneLine(redactErrorText(value))
	// Strip terminal control characters from persisted free-form explanations.
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r >= 128 && r <= 159 {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return value
}

func renderInspectFailureOverview(w io.Writer, failure *inspectFailureOverview) {
	if failure == nil {
		return
	}
	label := "Failed deployment"
	if failure.Candidate {
		label = "Failed candidate (serving release remains active)"
	}
	_, _ = fmt.Fprintf(w, "  %s: %s\n", label, compactInspectFailureText(failure.DeploymentID))
	if failure.Cause != "" {
		_, _ = fmt.Fprintf(w, "  Persisted cause: %s\n", failure.Cause)
	}
	if failure.Recovery != "" {
		_, _ = fmt.Fprintf(w, "  Recovery: %s\n", failure.Recovery)
	}
}
