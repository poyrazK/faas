package reposcan

import (
	"fmt"
	"sort"
	"strings"
)

const (
	detectionOutcomeMerged  = "merged"
	detectionOutcomeSkipped = "skipped"
)

// DetectionWarning is one detector decision that did not become a standalone
// workload. It is separate from Result.Warnings so the legacy human-readable
// warning list remains wire-compatible while --explain gets structured data.
type DetectionWarning struct {
	Workload string
	Detector string
	Marker   string
	Priority uint8
	Outcome  string
	Reason   string
}

type detectionCandidate struct {
	detector detector
	marker   string
}

// detectionMarker reduces the existing provenance string to the concrete
// source marker a client can display without parsing a workload name out of it.
// For convention detection the directory itself is the marker; for manifest
// based detectors it is the manifest path/name.
func detectionMarker(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	if source == "root-floor" {
		return source
	}
	if i := strings.Index(source, ": "); i >= 0 {
		marker := strings.TrimSpace(source[:i])
		if marker == "convention" {
			if member := strings.TrimSpace(source[i+2:]); member != "" {
				return member
			}
		}
		return marker
	}
	return source
}

func detectionWarningFromString(det detector, raw string) DetectionWarning {
	warning := DetectionWarning{
		Detector: det.String(),
		Priority: det.priority(),
		Outcome:  detectionOutcomeSkipped,
		Reason:   raw,
	}
	body := strings.TrimSpace(strings.TrimPrefix(raw, "reposcan:"))
	if i := strings.Index(body, ": "); i >= 0 {
		source := strings.TrimSpace(body[:i])
		rest := strings.TrimSpace(body[i+2:])
		warning.Marker = detectionMarker(source)
		warning.Workload = warningWorkload(rest)
		warning.Reason = rest
		if warning.Workload != "" {
			warning.Reason = strings.TrimSpace(strings.TrimPrefix(rest, warning.Workload))
			warning.Reason = strings.TrimSpace(strings.TrimPrefix(warning.Reason, "skipped"))
		}
		if source == "convention" {
			// Convention warnings name the directory member immediately
			// after the detector label, so it is the useful marker.
			if fields := strings.Fields(rest); len(fields) > 0 {
				warning.Marker = strings.Trim(fields[0], "\"'")
			}
		}
	}
	return warning
}

func warningWorkload(rest string) string {
	if i := strings.Index(rest, `function "`); i >= 0 {
		name := rest[i+len(`function "`):]
		if end := strings.IndexByte(name, '"'); end >= 0 {
			return name[:end]
		}
	}
	if i := strings.Index(rest, "refusing StatefulSet "); i >= 0 {
		fields := strings.Fields(rest[i+len("refusing StatefulSet "):])
		if len(fields) > 0 {
			return strings.Trim(fields[0], "\"'")
		}
	}
	if i := strings.Index(rest, " for "); i >= 0 {
		name := strings.Fields(rest[i+len(" for "):])
		if len(name) > 0 {
			return strings.Trim(name[0], "\"'")
		}
	}
	if strings.HasPrefix(rest, "no usable") || strings.HasPrefix(rest, "triggers ") {
		return ""
	}
	if fields := strings.Fields(rest); len(fields) > 0 {
		candidate := strings.Trim(fields[0], "\"'")
		if candidate != "unsupported" && candidate != "refusing" {
			return candidate
		}
	}
	return ""
}

func mergedDetectionWarnings(workloads []Workload) []DetectionWarning {
	var warnings []DetectionWarning
	for _, workload := range workloads {
		for _, candidate := range workload.DetectedBy.mergedCandidates {
			warnings = append(warnings, DetectionWarning{
				Workload: workload.Name,
				Detector: candidate.detector.String(),
				Marker:   candidate.marker,
				Priority: candidate.detector.priority(),
				Outcome:  detectionOutcomeMerged,
				Reason: fmt.Sprintf(
					"matched root_dir=%q and name=%q; %s won identity with priority %d",
					workload.RootDir, workload.Name,
					workload.DetectedBy.Detector, workload.DetectedBy.Priority),
			})
		}
	}
	return warnings
}

func sortDetectionWarnings(warnings []DetectionWarning) {
	sort.SliceStable(warnings, func(i, j int) bool {
		if strings.ToLower(warnings[i].Workload) != strings.ToLower(warnings[j].Workload) {
			return strings.ToLower(warnings[i].Workload) < strings.ToLower(warnings[j].Workload)
		}
		if warnings[i].Outcome != warnings[j].Outcome {
			return warnings[i].Outcome < warnings[j].Outcome
		}
		if warnings[i].Detector != warnings[j].Detector {
			return warnings[i].Detector < warnings[j].Detector
		}
		if warnings[i].Marker != warnings[j].Marker {
			return warnings[i].Marker < warnings[j].Marker
		}
		return warnings[i].Reason < warnings[j].Reason
	})
}
