package openapidiff

import (
	"fmt"
	"sort"
	"strings"
)

// RoutePairFinding describes one declared compatibility concern for a mapped
// source/successor operation. Locations are limited to contract pointers; raw
// schema values and examples are never copied into findings.
type RoutePairFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Location string `json:"location,omitempty"`
}

// RoutePairComparison compares two explicitly mapped operations. A
// no_supported_breaks result means the supported contract checks found no
// breaking difference; it does not establish behavioral equivalence.
type RoutePairComparison struct {
	Status   string             `json:"status"`
	Findings []RoutePairFinding `json:"findings"`
}

// CompareRoutePair compares response schemas, declared request constraints,
// security requirements, HTTP methods, and path-parameter shape for one
// explicitly mapped operation pair. Different literal route names are
// expected in a migration mapping; path parameters are aligned by position so
// renamed placeholders can still be compared.
func CompareRoutePair(baseline *Spec, baselineMethod, baselinePath string, candidate *Spec, candidateMethod, candidatePath string) (RoutePairComparison, error) {
	result := RoutePairComparison{Findings: []RoutePairFinding{}}
	if baseline == nil || candidate == nil {
		result.add("unknown", "contract_unavailable", "")
		result.finish()
		return result, nil
	}
	baselineMethod = strings.ToLower(strings.TrimSpace(baselineMethod))
	candidateMethod = strings.ToLower(strings.TrimSpace(candidateMethod))
	baselineItem, baselineOperation := routePairOperation(baseline, baselinePath, baselineMethod)
	candidateItem, candidateOperation := routePairOperation(candidate, candidatePath, candidateMethod)
	if baselineOperation == nil {
		result.add("unknown", "source_operation_not_found", "")
	}
	if candidateOperation == nil {
		result.add("unknown", "successor_operation_not_found", "")
	}
	if baselineOperation == nil || candidateOperation == nil {
		result.finish()
		return result, nil
	}
	if baselineMethod != candidateMethod {
		result.add("breaking", "method_changed", "/method")
	}

	baselineNames, candidateNames := routePairPathParameters(baselinePath), routePairPathParameters(candidatePath)
	if !equalRoutePairInts(routePairPathParameterPositions(baselinePath), routePairPathParameterPositions(candidatePath)) {
		result.add("unknown", "path_parameter_position_changed", "/parameters/path")
	}
	if len(candidateNames) > len(baselineNames) {
		result.add("breaking", "path_parameter_added", "/parameters/path")
	}
	if len(candidateNames) < len(baselineNames) {
		result.add("unknown", "path_parameter_removed", "/parameters/path")
	}
	baselineRename, candidateRename := map[string]string{}, map[string]string{}
	for i, name := range baselineNames {
		canonical := fmt.Sprintf("__gregale_path_%d", i+1)
		baselineRename[name] = canonical
	}
	for i, name := range candidateNames {
		candidateRename[name] = fmt.Sprintf("__gregale_path_%d", i+1)
	}
	canonicalPath := routePairCanonicalPath(max(len(baselineNames), len(candidateNames)))
	baseView := routePairSpec(baseline, baselineItem, baselineOperation, baselineRename, canonicalPath)
	candidateView := routePairSpec(candidate, candidateItem, candidateOperation, candidateRename, canonicalPath)

	for _, change := range Compare(baseView, candidateView) {
		location := "/responses/" + routePairPointer(change.Status)
		if change.PathInSchema != "" {
			location += "/" + routePairPointer(change.PathInSchema)
		}
		result.add("breaking", "response_"+string(change.Kind), location)
	}
	requests, err := CompareRequests(baseView, candidateView)
	if err != nil {
		result.add("unknown", "request_comparison_incomplete", "/request")
	} else {
		if len(requests.Routes) != 1 || !requests.Routes[0].Complete {
			result.add("unknown", "request_contract_incomplete", "/request")
		}
		for _, route := range requests.Routes {
			for _, finding := range route.Findings {
				if routePairDuplicatePathParameterFinding(finding, baselineNames, candidateNames) {
					continue
				}
				result.add(finding.Severity, finding.Code, routePairPublicLocation(finding.Location, baselineNames, candidateNames))
			}
		}
	}

	security, err := CompareSecurity(baseView, candidateView)
	if err != nil {
		result.add("unknown", "security_comparison_incomplete", "/security")
	} else {
		if len(security.Routes) != 1 || !security.Routes[0].Complete {
			result.add("unknown", "security_contract_incomplete", "/security")
		}
		for _, route := range security.Routes {
			for _, finding := range route.Findings {
				severity := finding.Severity
				switch severity {
				case "client_breaking":
					severity = "breaking"
				case "regression":
					severity = "review"
				}
				result.add(severity, finding.Code, finding.Location)
			}
		}
	}
	result.finish()
	return result, nil
}

func (result *RoutePairComparison) add(severity, code, location string) {
	if severity != "breaking" && severity != "review" && severity != "unknown" {
		severity = "unknown"
	}
	finding := RoutePairFinding{Severity: severity, Code: code, Location: location}
	for _, existing := range result.Findings {
		if existing == finding {
			return
		}
	}
	result.Findings = append(result.Findings, finding)
}

func (result *RoutePairComparison) finish() {
	sort.Slice(result.Findings, func(i, j int) bool {
		a, b := result.Findings[i], result.Findings[j]
		return a.Severity+"\x00"+a.Code+"\x00"+a.Location < b.Severity+"\x00"+b.Code+"\x00"+b.Location
	})
	switch {
	case routePairHasSeverity(result.Findings, "breaking"):
		result.Status = "breaking"
	case routePairHasSeverity(result.Findings, "unknown"):
		result.Status = "unknown"
	case routePairHasSeverity(result.Findings, "review"):
		result.Status = "review_required"
	default:
		result.Status = "no_supported_breaks"
	}
}

func routePairHasSeverity(findings []RoutePairFinding, severity string) bool {
	for _, finding := range findings {
		if finding.Severity == severity {
			return true
		}
	}
	return false
}

func routePairOperation(spec *Spec, path, method string) (*PathItem, *Operation) {
	if spec == nil || !strings.HasPrefix(path, "/") {
		return nil, nil
	}
	item := spec.Paths[path]
	if item == nil {
		return nil, nil
	}
	return item, item.Methods[method]
}

func routePairPathParameters(path string) []string {
	var names []string
	for len(path) > 0 {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			break
		}
		path = path[start+1:]
		end := strings.IndexByte(path, '}')
		if end < 0 {
			break
		}
		name := path[:end]
		if name == "" || strings.ContainsAny(name, "{}") {
			return nil
		}
		names = append(names, name)
		path = path[end+1:]
	}
	return names
}

func routePairPathParameterPositions(path string) []int {
	segments := strings.Split(path, "/")
	positions := make([]int, 0)
	for index, segment := range segments {
		if strings.Contains(segment, "{") || strings.Contains(segment, "}") {
			positions = append(positions, index)
		}
	}
	return positions
}

func equalRoutePairInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func routePairPublicLocation(location string, baselineNames, candidateNames []string) string {
	for index := 0; index < max(len(baselineNames), len(candidateNames)); index++ {
		name := ""
		if index < len(candidateNames) {
			name = candidateNames[index]
		} else {
			name = baselineNames[index]
		}
		location = strings.ReplaceAll(location, "/"+fmt.Sprintf("__gregale_path_%d", index+1), "/"+routePairPointer(name))
	}
	return location
}

func routePairDuplicatePathParameterFinding(finding RequestFinding, baselineNames, candidateNames []string) bool {
	if finding.Code == "path_parameter_added" && len(candidateNames) > len(baselineNames) {
		return true
	}
	return finding.Code == "parameter_removed" && len(candidateNames) < len(baselineNames) && strings.HasPrefix(finding.Location, "/parameters/path/")
}

func routePairCanonicalPath(parameters int) string {
	path := "/__gregale_migration_pair__"
	for i := 0; i < parameters; i++ {
		path += fmt.Sprintf("/{__gregale_path_%d}", i+1)
	}
	return path
}

func routePairSpec(source *Spec, item *PathItem, operation *Operation, renames map[string]string, canonicalPath string) *Spec {
	pathRaw := routePairRenameParameters(item.Raw, renames)
	operationRaw := routePairRenameParameters(operation.Raw, renames)
	for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
		delete(pathRaw, method)
	}
	pathRaw["get"] = operationRaw
	operationCopy := *operation
	operationCopy.Raw = operationRaw
	itemCopy := &PathItem{Raw: pathRaw, Methods: map[string]*Operation{"get": &operationCopy}, Parameters: item.Parameters}
	return &Spec{
		Raw: source.Raw, Paths: map[string]*PathItem{canonicalPath: itemCopy},
		Components: source.Components, version: source.version,
	}
}

func routePairRenameParameters(raw map[string]any, renames map[string]string) map[string]any {
	cloned := make(map[string]any, len(raw))
	for key, value := range raw {
		cloned[key] = value
	}
	for _, key := range []string{"parameters"} {
		values, ok := raw[key].([]any)
		if !ok {
			continue
		}
		copyValues := make([]any, len(values))
		for i, value := range values {
			parameter, ok := value.(map[string]any)
			if !ok {
				copyValues[i] = value
				continue
			}
			parameterCopy := make(map[string]any, len(parameter))
			for name, field := range parameter {
				parameterCopy[name] = field
			}
			if parameterCopy["in"] == "path" {
				if name, ok := parameterCopy["name"].(string); ok {
					if canonical, found := renames[name]; found {
						parameterCopy["name"] = canonical
					}
				}
			}
			copyValues[i] = parameterCopy
		}
		cloned[key] = copyValues
	}
	return cloned
}

func routePairPointer(value string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(value)
}
