// Package routecontract compares statically discovered application routes
// with the operations declared in a local OpenAPI document.
package routecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

const Version = 1
const maxCandidatesPerFinding = 20

var parameterSegment = regexp.MustCompile(`^\{[A-Za-z0-9_.-]+\}$`)

type Candidate struct {
	Path         string                `json:"path"`
	Handler      string                `json:"handler"`
	Source       *routeimpact.Location `json:"source,omitempty"`
	Registration *routeimpact.Location `json:"registration,omitempty"`
}

// Finding describes one operation in the union of the source and contract
// inventories. Parameter-shape matches are deliberately conservative: the
// normalized shape must be unique on both sides.
type Finding struct {
	Method            string                `json:"method"`
	ContractPath      string                `json:"contract_path,omitempty"`
	SourcePath        string                `json:"source_path,omitempty"`
	Status            string                `json:"status"`
	Match             string                `json:"match,omitempty"`
	Handler           string                `json:"handler,omitempty"`
	Source            *routeimpact.Location `json:"source,omitempty"`
	Registration      *routeimpact.Location `json:"registration,omitempty"`
	Candidates        []Candidate           `json:"candidates,omitempty"`
	CandidatesOmitted int                   `json:"candidates_omitted,omitempty"`
	Reason            string                `json:"reason,omitempty"`
}

type Summary struct {
	ContractRoutes int `json:"contract_routes"`
	SourceRoutes   int `json:"source_routes"`
	Matched        int `json:"matched"`
	SourceOnly     int `json:"source_only"`
	ContractOnly   int `json:"contract_only"`
	Unknown        int `json:"unknown"`
}

type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type Report struct {
	Version               int                 `json:"version"`
	Status                string              `json:"status"`
	Outcome               string              `json:"outcome"`
	App                   string              `json:"app,omitempty"`
	Framework             string              `json:"framework"`
	SourceRoot            string              `json:"source_root"`
	BaseRevision          string              `json:"base_revision"`
	Candidate             string              `json:"candidate_revision"`
	OpenAPIFile           string              `json:"openapi_file"`
	OpenAPIVersion        string              `json:"openapi_version"`
	OpenAPISHA256         string              `json:"openapi_sha256"`
	Summary               Summary             `json:"summary"`
	Routes                []Finding           `json:"routes"`
	SourceIssues          []routeimpact.Issue `json:"source_issues"`
	ContractIssues        []Issue             `json:"contract_issues"`
	ContractIssuesOmitted int                 `json:"contract_issues_omitted,omitempty"`
}

type operation struct {
	method string
	path   string
	shape  string
}

type sourceOperation struct {
	method       string
	path         string
	shape        string
	handler      string
	source       *routeimpact.Location
	registration *routeimpact.Location
}

// Compare parses a bounded OpenAPI 3.0/3.1 document and compares its operations
// with the candidate routes from a routeimpact report. A syntactically valid
// but unresolved Path Item reference or incomplete source analysis makes
// unmatched routes inconclusive instead of reporting drift.
func Compare(document []byte, filename string, source routeimpact.Report) (Report, error) {
	if len(document) == 0 || len(document) > api.RouteImpactSourceMaxBytes {
		return Report{}, errors.New("OpenAPI document is empty or exceeds its byte limit")
	}
	if source.Status != "complete" && source.Status != "incomplete" {
		return Report{}, errors.New("source route analysis has an unsupported status")
	}
	spec, err := openapidiff.LoadBytes(document)
	if err != nil {
		return Report{}, errors.New("OpenAPI document could not be parsed")
	}
	version := spec.OpenAPIVersion()
	if !supportedOpenAPIVersion(version) {
		return Report{}, errors.New("OpenAPI document must declare version 3.0.x or 3.1.x")
	}
	rawPaths, ok := spec.Raw["paths"].(map[string]any)
	if !ok {
		return Report{}, errors.New("OpenAPI document must contain a paths object")
	}

	contractRoutes := make([]operation, 0)
	contractIssues := make([]Issue, 0)
	contractIssuesOmitted := 0
	addContractIssue := func(issue Issue) {
		if len(contractIssues) < api.RouteImpactMaxIssues {
			contractIssues = append(contractIssues, issue)
		} else {
			contractIssuesOmitted++
		}
	}
	paths := make([]string, 0, len(rawPaths))
	for path := range rawPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	standardMethods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}
	for _, path := range paths {
		item, ok := rawPaths[path].(map[string]any)
		if !ok {
			return Report{}, errors.New("OpenAPI paths contains an invalid Path Item")
		}
		if !validPathTemplate(path) {
			addContractIssue(Issue{Code: "invalid_path_template", Path: path,
				Message: "The OpenAPI path is outside the supported absolute path template format."})
		} else if hasUnnormalizedParameters(path) {
			addContractIssue(Issue{Code: "unsupported_parameter_shape", Path: path,
				Message: "Embedded path parameters are not normalized; unmatched routes remain inconclusive."})
		}
		if _, referenced := item["$ref"]; referenced {
			addContractIssue(Issue{Code: "unresolved_path_item_reference", Path: path,
				Message: "A Path Item reference is not resolved by this local route check."})
		}
		itemKeys := make([]string, 0, len(item))
		for key := range item {
			itemKeys = append(itemKeys, key)
		}
		sort.Strings(itemKeys)
		for _, key := range itemKeys {
			rawOperation := item[key]
			lower := strings.ToLower(key)
			if !standardMethods[lower] {
				if strings.EqualFold(key, "connect") {
					addContractIssue(Issue{Code: "unsupported_method", Path: path,
						Message: "OpenAPI does not define a CONNECT operation field, so this method cannot be checked against the document."})
				}
				continue
			}
			if key != lower {
				addContractIssue(Issue{Code: "invalid_method_key", Path: path,
					Message: "OpenAPI operation method keys must be lowercase."})
				continue
			}
			if _, ok := rawOperation.(map[string]any); !ok {
				return Report{}, errors.New("OpenAPI Path Item contains a malformed operation")
			}
			contractRoutes = append(contractRoutes, operation{method: strings.ToUpper(key), path: path, shape: parameterShape(path)})
			if len(contractRoutes) > api.RouteImpactMaxRoutes {
				return Report{}, fmt.Errorf("OpenAPI document has more than %d operations; narrow the document before checking it", api.RouteImpactMaxRoutes)
			}
		}
	}
	if spec.Paths == nil {
		return Report{}, errors.New("OpenAPI paths could not be read")
	}

	sha := sha256.Sum256(document)
	report := Report{
		Version: Version, Status: "complete", Outcome: "clear", App: source.App,
		Framework: source.Framework, SourceRoot: source.SourceRoot,
		BaseRevision: source.Base.Revision, Candidate: source.Candidate.Revision,
		OpenAPIFile: filename, OpenAPIVersion: version, OpenAPISHA256: hex.EncodeToString(sha[:]),
		Summary: Summary{ContractRoutes: len(contractRoutes)}, Routes: []Finding{},
		SourceIssues: append([]routeimpact.Issue{}, source.Issues...), ContractIssues: contractIssues,
		ContractIssuesOmitted: contractIssuesOmitted,
	}

	sourceRoutes := make([]sourceOperation, 0)
	sourcePathShapesComplete := true
	for _, result := range source.Routes {
		if result.After == nil {
			continue
		}
		route := result.After
		if !validPathTemplate(route.Path) || hasUnnormalizedParameters(route.Path) {
			sourcePathShapesComplete = false
			file, line := route.Registration.File, route.Registration.Line
			report.SourceIssues = append(report.SourceIssues, routeimpact.Issue{Code: "unsupported_source_path_shape", File: file, Line: line,
				Message: "A source route path is outside the supported parameter template format; unmatched routes remain inconclusive."})
		}
		var sourceLocation, registrationLocation *routeimpact.Location
		if route.Source.File != "" && route.Source.Line > 0 {
			location := route.Source
			sourceLocation = &location
		}
		if route.Registration.File != "" && route.Registration.Line > 0 {
			location := route.Registration
			registrationLocation = &location
		}
		sourceRoutes = append(sourceRoutes, sourceOperation{
			method: strings.ToUpper(route.Method), path: route.Path, shape: parameterShape(route.Path),
			handler: route.Handler, source: sourceLocation, registration: registrationLocation,
		})
	}
	if len(sourceRoutes) > api.RouteImpactMaxRoutes {
		return Report{}, errors.New("source route inventory exceeds its route limit")
	}
	sort.Slice(sourceRoutes, func(i, j int) bool {
		if sourceRoutes[i].method != sourceRoutes[j].method {
			return sourceRoutes[i].method < sourceRoutes[j].method
		}
		return sourceRoutes[i].path < sourceRoutes[j].path
	})
	report.Summary.SourceRoutes = len(sourceRoutes)

	sourceByKey := make(map[string]int, len(sourceRoutes))
	contractMatched := make([]bool, len(contractRoutes))
	sourceMatched := make([]bool, len(sourceRoutes))
	sourceInventoryComplete := source.Status == "complete" && sourcePathShapesComplete
	for i, route := range sourceRoutes {
		key := route.method + "\x00" + route.path
		if _, duplicate := sourceByKey[key]; duplicate {
			sourceInventoryComplete = false
			file, line := "", 0
			if route.registration != nil {
				file, line = route.registration.File, route.registration.Line
			}
			report.SourceIssues = append(report.SourceIssues, routeimpact.Issue{Code: "duplicate_source_route", File: file, Line: line,
				Message: "Static analysis found duplicate registrations for one method and path; attribution is ambiguous."})
			continue
		}
		sourceByKey[key] = i
	}

	for i, contract := range contractRoutes {
		j, ok := sourceByKey[contract.method+"\x00"+contract.path]
		if !ok {
			continue
		}
		contractMatched[i], sourceMatched[j] = true, true
		report.Routes = append(report.Routes, matchedFinding(contract, sourceRoutes[j], "exact"))
		report.Summary.Matched++
	}

	contractShapes := map[string][]int{}
	sourceShapes := map[string][]int{}
	for i, route := range contractRoutes {
		if !contractMatched[i] && route.shape != route.path {
			key := route.method + "\x00" + route.shape
			contractShapes[key] = append(contractShapes[key], i)
		}
	}
	for i, route := range sourceRoutes {
		if !sourceMatched[i] && route.shape != route.path {
			key := route.method + "\x00" + route.shape
			sourceShapes[key] = append(sourceShapes[key], i)
		}
	}
	shapeKeys := make([]string, 0, len(contractShapes))
	for key := range contractShapes {
		if len(sourceShapes[key]) > 0 {
			shapeKeys = append(shapeKeys, key)
		}
	}
	sort.Strings(shapeKeys)
	ambiguous := false
	for _, key := range shapeKeys {
		contracts, sources := contractShapes[key], sourceShapes[key]
		if len(contracts) == 1 && len(sources) == 1 {
			i, j := contracts[0], sources[0]
			contractMatched[i], sourceMatched[j] = true, true
			report.Routes = append(report.Routes, matchedFinding(contractRoutes[i], sourceRoutes[j], "parameter_shape"))
			report.Summary.Matched++
			continue
		}
		ambiguous = true
		candidates := make([]Candidate, 0, min(len(sources), maxCandidatesPerFinding))
		for _, j := range sources {
			sourceMatched[j] = true
			if len(candidates) < maxCandidatesPerFinding {
				candidates = append(candidates, sourceCandidate(sourceRoutes[j]))
			}
		}
		for _, i := range contracts {
			contractMatched[i] = true
			report.Routes = append(report.Routes, Finding{Method: contractRoutes[i].method, ContractPath: contractRoutes[i].path,
				Status: "unknown", Candidates: candidates, CandidatesOmitted: max(0, len(sources)-len(candidates)),
				Reason: "Multiple parameterized routes share this path shape; no unique match can be established."})
			report.Summary.Unknown++
		}
	}

	inventoryComplete := sourceInventoryComplete && len(contractIssues) == 0 && contractIssuesOmitted == 0
	for i, route := range contractRoutes {
		if contractMatched[i] {
			continue
		}
		if inventoryComplete {
			report.Routes = append(report.Routes, Finding{Method: route.method, ContractPath: route.path, Status: "contract_only",
				Reason: "The OpenAPI operation has no statically discovered source registration."})
			report.Summary.ContractOnly++
		} else {
			report.Routes = append(report.Routes, Finding{Method: route.method, ContractPath: route.path, Status: "unknown",
				Reason: "The route inventory is incomplete, so absence from the source analysis is inconclusive."})
			report.Summary.Unknown++
		}
	}
	for i, route := range sourceRoutes {
		if sourceMatched[i] {
			continue
		}
		if inventoryComplete {
			report.Routes = append(report.Routes, sourceFinding(route, "source_only", "The source registration has no matching OpenAPI operation."))
			report.Summary.SourceOnly++
		} else {
			report.Routes = append(report.Routes, sourceFinding(route, "unknown", "The route inventory is incomplete, so absence from the OpenAPI contract is inconclusive."))
			report.Summary.Unknown++
		}
	}

	sort.Slice(report.Routes, func(i, j int) bool {
		a, b := report.Routes[i], report.Routes[j]
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.ContractPath != b.ContractPath {
			return a.ContractPath < b.ContractPath
		}
		return a.SourcePath < b.SourcePath
	})
	if !sourceInventoryComplete || len(report.ContractIssues) > 0 || report.ContractIssuesOmitted > 0 || ambiguous {
		report.Status = "incomplete"
		report.Outcome = "incomplete"
	} else if report.Summary.SourceOnly+report.Summary.ContractOnly > 0 {
		report.Outcome = "drift"
	}
	return report, nil
}

func supportedOpenAPIVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "3" || (parts[1] != "0" && parts[1] != "1") || parts[2] == "" {
		return false
	}
	for _, char := range parts[2] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parameterShape(path string) string {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return path
	}
	segments := strings.Split(path, "/")
	changed := false
	for i, segment := range segments {
		if strings.ContainsAny(segment, "{}") {
			if !parameterSegment.MatchString(segment) {
				return path
			}
			segments[i] = "{}"
			changed = true
		}
	}
	if !changed {
		return path
	}
	return strings.Join(segments, "/")
}

func hasUnnormalizedParameters(path string) bool {
	return strings.ContainsAny(path, "{}") && parameterShape(path) == path
}

func validPathTemplate(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\\") {
		return false
	}
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '{':
			end := strings.IndexByte(path[i+1:], '}')
			if end < 0 {
				return false
			}
			end += i + 1
			if !parameterSegment.MatchString(path[i : end+1]) {
				return false
			}
			i = end
		case '}':
			return false
		}
	}
	return true
}

func matchedFinding(contract operation, source sourceOperation, match string) Finding {
	return Finding{Method: contract.method, ContractPath: contract.path, SourcePath: source.path, Status: "matched", Match: match,
		Handler: source.handler, Source: source.source, Registration: source.registration}
}

func sourceCandidate(source sourceOperation) Candidate {
	return Candidate{Path: source.path, Handler: source.handler, Source: source.source, Registration: source.registration}
}

func sourceFinding(source sourceOperation, status, reason string) Finding {
	return Finding{Method: source.method, SourcePath: source.path, Status: status, Handler: source.handler,
		Source: source.source, Registration: source.registration, Reason: reason}
}
