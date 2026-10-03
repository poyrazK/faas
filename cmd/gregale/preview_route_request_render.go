package main

import (
	"fmt"
	"io"
)

func renderPreviewRequestFindings(w io.Writer, report previewRouteReport, markdown bool) {
	if report.Requests.Status == "" {
		return
	}
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Request contract findings\n\n")
	} else {
		_, _ = fmt.Fprint(w, "\nRequest contract findings\n")
	}
	_, _ = fmt.Fprint(w, "Comparison covers supported declared input restrictions. It does not verify application validation or business behavior. Unknown findings require review.\n\n")
	count := 0
	for _, route := range report.Routes {
		if route.RequestCompatibility == nil {
			continue
		}
		for _, finding := range route.RequestCompatibility.Findings {
			count++
			detail := previewSourceDisplay(finding.Location, markdown)
			if finding.Revision != "" {
				detail += "; " + finding.Revision
			}
			_, _ = fmt.Fprintf(w, "- %s: %s; %s (%s)\n", previewSourceDisplay(previewReportRouteKey(route.Method, route.Path), markdown), finding.Severity, requestFindingDescription(finding.Code), detail)
		}
	}
	if count == 0 && report.Requests.Status == "available" {
		_, _ = fmt.Fprintln(w, "No supported request restrictions were found in compared operations.")
	}
}

func requestFindingDescription(code string) string {
	switch code {
	case "request_body_required":
		return "A request body became required."
	case "parameter_required":
		return "This parameter became required."
	case "property_required":
		return "This request field became required."
	case "accepted_type_narrowed":
		return "The declared set of accepted input types narrowed."
	case "null_no_longer_allowed":
		return "Previously allowed null input is no longer accepted by the declared schema."
	case "enum_values_restricted":
		return "The accepted values were restricted by an enum."
	case "numeric_minimum_restricted":
		return "The minimum accepted numeric input became stricter."
	case "numeric_maximum_restricted":
		return "The maximum accepted numeric input became stricter."
	case "string_min_length_restricted":
		return "The minimum accepted string length increased."
	case "string_max_length_restricted":
		return "The maximum accepted string length decreased."
	case "array_min_items_restricted":
		return "The minimum accepted array size increased."
	case "array_max_items_restricted":
		return "The maximum accepted array size decreased."
	case "content_type_removed":
		return "A previously accepted content type is no longer covered."
	case "schema_rejects_all":
		return "The proposed schema declares no accepted values."
	case "additional_properties_restricted":
		return "Extra object properties are no longer accepted."
	case "property_no_longer_allowed":
		return "This object property is no longer allowed."
	case "request_body_removed":
		return "The request-body declaration was removed; acceptance cannot be determined."
	case "parameter_removed":
		return "The parameter declaration was removed; acceptance cannot be determined."
	case "path_parameter_added":
		return "This path parameter lacked a baseline declaration; input compatibility is unresolved."
	default:
		return "Request comparison is unresolved: " + code + "."
	}
}
