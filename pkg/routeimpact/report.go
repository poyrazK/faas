package routeimpact

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// ParseReport reads a customer-owned version 2 artifact. It validates bounds
// and internal structure, not authenticity or runtime behavior. Errors never
// include arbitrary input values or decoder excerpts.
func ParseReport(body []byte) (Report, error) {
	if len(body) > api.RouteImpactReportMaxBytes {
		return Report{}, errors.New("source impact report exceeds its byte limit")
	}
	if err := validateJSONKeys(json.NewDecoder(bytes.NewReader(body)), 0); err != nil {
		return Report{}, errors.New("source impact report has invalid, duplicate, or excessively nested JSON fields")
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Report{}, errors.New("source impact report has invalid JSON or unknown fields")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Report{}, errors.New("source impact report must contain one JSON document")
	}
	if err := validateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func validMetadata(value string) bool {
	if len(value) > api.RouteImpactMetadataMaxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// Decode tokens first so duplicate object keys cannot silently replace
// provenance or results in the typed decoder. Never expose decoder excerpts.
func validateJSONKeys(decoder *json.Decoder, depth int) error {
	if depth > api.RouteImpactReportJSONMaxDepth {
		return errors.New("JSON depth limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			name = strings.ToLower(name)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[name] = true
			if err := validateJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON container")
	}
	_, err = decoder.Token()
	return err
}

func validSourcePath(value string) bool {
	return value != "" && value != "." && validMetadata(value) && fs.ValidPath(value) && !strings.Contains(value, "\\")
}

func validLocation(location Location) bool {
	return validSourcePath(location.File) && location.Line > 0 && location.Line <= api.RouteImpactFileMaxBytes
}

func validSymbolLocation(location SymbolLocation) bool {
	return location.Name != "" && validMetadata(location.Name) && validLocation(Location{File: location.File, Line: location.Line})
}

func validSnapshot(snapshot Snapshot, candidate bool) bool {
	_, err := hex.DecodeString(snapshot.SourceSHA256)
	return (ValidCommit(snapshot.Revision) || (candidate && snapshot.Revision == "working-tree")) &&
		len(snapshot.SourceSHA256) == 64 && err == nil && snapshot.PythonFiles >= 0 && snapshot.PythonFiles <= api.RouteImpactMaxPythonFiles &&
		(snapshot.Entrypoint == "" || validEntrypoint(snapshot.Entrypoint))
}

func validateReport(report Report) error {
	if report.Version != 2 || report.Framework != "fastapi" || (report.Status != "complete" && report.Status != "incomplete") {
		return errors.New("source impact requires a version 2 FastAPI report")
	}
	identity, _ := RepositoryReference(report.Repository)
	if report.Repository != "" && identity != report.Repository {
		return errors.New("source impact report repository must be a canonical credential-free identity")
	}
	if !validMetadata(report.App) || !validMetadata(report.Scope) || !validMetadata(report.SourceRoot) ||
		(report.SourceRoot != "." && !validSourcePath(report.SourceRoot)) || !validSnapshot(report.Base, false) || !validSnapshot(report.Candidate, true) {
		return errors.New("source impact report has invalid provenance metadata")
	}
	if report.Routes == nil || report.Issues == nil || report.ChangedFiles == nil || report.ChangedSymbols == nil ||
		len(report.Routes) > api.RouteImpactMaxComparedRoutes || len(report.ChangedFiles) > 2*api.RouteImpactMaxPaths ||
		len(report.ChangedSymbols) > 2*api.RouteImpactMaxSymbols || len(report.Issues) > api.RouteImpactMaxReportIssues {
		return errors.New("source impact report has missing collections or exceeds collection bounds")
	}
	for _, change := range report.ChangedFiles {
		if !validSourcePath(change.File) || !validFileChange(change.Change) {
			return errors.New("source impact report has invalid changed-file metadata")
		}
	}
	for _, change := range report.ChangedSymbols {
		if change.Name == "" || !validMetadata(change.Name) || !validFileChange(change.Change) ||
			(change.Before == nil && change.After == nil) || (change.Before != nil && !validSymbolLocation(*change.Before)) ||
			(change.After != nil && !validSymbolLocation(*change.After)) ||
			(change.Before != nil && change.Before.Name != change.Name) || (change.After != nil && change.After.Name != change.Name) ||
			(change.Change == "added" && change.Before != nil) || (change.Change == "removed" && change.After != nil) ||
			(change.Change == "modified" && (change.Before == nil || change.After == nil)) {
			return errors.New("source impact report has invalid changed-symbol metadata")
		}
	}
	seen := map[string]bool{}
	summary := Summary{}
	evidenceCount, uncertaintyCount, evidenceBytes := 0, 0, 0
	for _, result := range report.Routes {
		key := result.Method + " " + result.Path
		if seen[key] || !validMethod(result.Method) || !validRoutePath(result.Path) || !validResult(result) {
			return errors.New("source impact report has invalid or duplicate route results")
		}
		seen[key] = true
		switch result.Change {
		case "added":
			summary.Added++
		case "removed":
			summary.Removed++
		case "source_changed":
			summary.SourceChanged++
		case "potentially_affected":
			summary.PotentiallyAffected++
		case "no_linked_changes":
			summary.NoLinkedChanges++
		case "unknown":
			summary.Unknown++
		default:
			return errors.New("source impact report has an invalid change classification")
		}
		for _, evidence := range result.Evidence {
			if !validEvidence(evidence) {
				return errors.New("source impact report has invalid reference evidence")
			}
			evidenceCount++
			evidenceBytes += len(evidence.File) + len(evidence.Symbol)
			for _, file := range evidence.Via {
				evidenceBytes += len(file)
			}
			for _, location := range evidence.ViaSymbols {
				evidenceBytes += len(location.File) + len(location.Name)
			}
		}
		for _, issue := range result.Uncertainties {
			if !validIssue(issue) {
				return errors.New("source impact report has invalid uncertainties")
			}
			uncertaintyCount++
			evidenceBytes += len(issue.File) + len(issue.Symbol) + len(issue.Message)
		}
	}
	for _, issue := range report.Issues {
		if !validIssue(issue) {
			return errors.New("source impact report has invalid issues")
		}
	}
	if summary != report.Summary || evidenceCount > api.RouteImpactMaxEvidence || uncertaintyCount > api.RouteImpactMaxEvidence || evidenceBytes > api.RouteImpactEvidenceMaxBytes {
		return errors.New("source impact report summary disagrees with routes or evidence exceeds bounds")
	}
	if report.Status == "complete" && (len(report.Issues) > 0 || uncertaintyCount > 0 || summary.Unknown > 0) {
		return errors.New("source impact report claims completeness despite unresolved evidence")
	}
	return nil
}

func validMethod(value string) bool {
	switch value {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "TRACE":
		return true
	}
	return false
}

func validRoutePath(value string) bool {
	return strings.HasPrefix(value, "/") && validMetadata(value)
}

func validFileChange(value string) bool {
	return value == "added" || value == "removed" || value == "modified"
}

func validResult(result Result) bool {
	if result.Precision != "function" && result.Precision != "mixed" && result.Precision != "module_fallback" {
		return false
	}
	if (result.Before == nil && result.After == nil) || (result.Change == "added" && (result.Before != nil || result.After == nil)) ||
		(result.Change == "removed" && (result.Before == nil || result.After != nil)) ||
		(result.Change != "added" && result.Change != "removed" && result.Change != "unknown" && (result.Before == nil || result.After == nil)) {
		return false
	}
	for _, route := range []*Route{result.Before, result.After} {
		if route == nil {
			continue
		}
		if route.Method != result.Method || route.Path != result.Path || route.Handler == "" || !validMetadata(route.Handler) || !validMetadata(route.HandlerSymbol) ||
			!validLocation(route.Source) || !validLocation(route.Registration) || !validPaths(route.ContextFiles) || !validPaths(route.DependencyFiles) ||
			!validPaths(route.FallbackFiles) || len(route.DependencySymbols) > api.RouteImpactMaxSymbols {
			return false
		}
		for _, name := range route.DependencySymbols {
			if name == "" || !validMetadata(name) {
				return false
			}
		}
	}
	return true
}

func validPaths(values []string) bool {
	if len(values) > api.RouteImpactMaxPythonFiles {
		return false
	}
	for _, value := range values {
		if !validSourcePath(value) {
			return false
		}
	}
	return true
}

func validEvidence(evidence Evidence) bool {
	if !validSourcePath(evidence.File) || !validFileChange(evidence.Change) || (evidence.Revision != "base" && evidence.Revision != "candidate") ||
		!validMetadata(evidence.Symbol) || len(evidence.Via) > api.RouteImpactMaxGraphDepth || !validPaths(evidence.Via) || len(evidence.ViaSymbols) > api.RouteImpactMaxGraphDepth {
		return false
	}
	if evidence.Kind != "function_reference" && evidence.Kind != "module_initialization" && evidence.Kind != "module_fallback" {
		return false
	}
	if evidence.Kind == "function_reference" && (evidence.Symbol == "" || len(evidence.ViaSymbols) == 0) {
		return false
	}
	for _, location := range evidence.ViaSymbols {
		if !validSymbolLocation(location) {
			return false
		}
	}
	return evidence.Line >= 0 && evidence.Line <= api.RouteImpactFileMaxBytes
}

func validIssue(issue Issue) bool {
	return issue.Code != "" && validMetadata(issue.Code) && validMetadata(issue.Message) && validMetadata(issue.Symbol) &&
		(issue.File == "" || validSourcePath(issue.File)) && issue.Line >= 0 && issue.Line <= api.RouteImpactFileMaxBytes &&
		(issue.Revision == "" || issue.Revision == "base" || issue.Revision == "candidate")
}
