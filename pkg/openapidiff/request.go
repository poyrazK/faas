package openapidiff

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// RequestComparison describes declared input restrictions, not observed
// application behavior. No schema values, examples, defaults, or raw refs are
// returned. A known restriction can coexist with unresolved evidence.
type RequestComparison struct {
	Routes []RequestRoute `json:"routes"`
}

type RequestRoute struct {
	Method   string           `json:"method"`
	Path     string           `json:"path"`
	Status   string           `json:"status"`
	Complete bool             `json:"complete"`
	Changed  bool             `json:"changed"`
	Findings []RequestFinding `json:"findings"`
}

type RequestFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Location string `json:"location"`
	Revision string `json:"revision,omitempty"`
}

var ErrRequestComparisonLimit = errors.New("request compatibility comparison exceeds its work or output limit")

// CompareRequests compares operations present in both documents. Additions
// and removals remain the route/response classifier's responsibility. Local
// references resolve only in the supplied document; nothing is fetched.
func CompareRequests(base, candidate *Spec) (RequestComparison, error) {
	if base == nil || candidate == nil {
		return RequestComparison{}, errors.New("request compatibility requires both captured documents")
	}
	work := &requestWork{}
	result := RequestComparison{Routes: []RequestRoute{}}
	keys := map[string]bool{}
	for _, spec := range []*Spec{base, candidate} {
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			if _, referenced := item.Raw["$ref"]; referenced {
				return RequestComparison{}, errors.New("request compatibility cannot resolve referenced path items")
			}
			for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"} {
				if _, declared := item.Raw[method]; declared && item.Methods[method] == nil {
					return RequestComparison{}, errors.New("request compatibility requires parsed HTTP operations")
				}
			}
			for method := range item.Methods {
				keys[path+"\x00"+method] = true
				if len(keys) > api.RequestCompatibilityMaxRoutes {
					return RequestComparison{}, ErrRequestComparisonLimit
				}
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		path, method, _ := strings.Cut(key, "\x00")
		row := RequestRoute{Method: strings.ToUpper(method), Path: path, Status: "not_compared", Findings: []RequestFinding{}}
		before, after := requestOperation(base, path, method), requestOperation(candidate, path, method)
		if before == nil || after == nil {
			result.Routes = append(result.Routes, row)
			continue
		}
		row.Complete = true
		if !requestMetadata(path) || !strings.HasPrefix(path, "/") {
			work.unknown(&row, "invalid_route_metadata", "", "")
		} else if (!strings.HasPrefix(base.version, "3.0.") && !strings.HasPrefix(base.version, "3.1.")) ||
			(!strings.HasPrefix(candidate.version, "3.0.") && !strings.HasPrefix(candidate.version, "3.1.")) {
			work.unknown(&row, "unsupported_openapi_version", "", "")
		} else {
			old := requestParser{spec: base, work: work, row: &row, revision: "base"}
			new := requestParser{spec: candidate, work: work, row: &row, revision: "candidate"}
			if value, present := before.Raw["requestBody"]; present && value == nil {
				old.unknown("invalid_request_body", "/requestBody")
			}
			if value, present := after.Raw["requestBody"]; present && value == nil {
				new.unknown("invalid_request_body", "/requestBody")
			}
			oldBody, newBody := old.body(before.Raw["requestBody"]), new.body(after.Raw["requestBody"])
			oldParams, oldIdentitiesKnown := old.parameters(base.Paths[path].Raw, before.Raw)
			newParams, newIdentitiesKnown := new.parameters(candidate.Paths[path].Raw, after.Raw)
			row.Changed = !reflect.DeepEqual(oldBody, newBody) || !requestParametersEqual(oldParams, newParams)
			work.compareBody(&row, oldBody, newBody)
			for _, name := range requestSortedKeys(oldParams) {
				if newIdentitiesKnown && newParams[name] == nil {
					work.unknown(&row, "parameter_removed", oldParams[name].location, "")
				}
			}
			for _, name := range requestSortedKeys(newParams) {
				if !oldIdentitiesKnown || !newIdentitiesKnown {
					continue
				}
				proposed := newParams[name]
				baseline := oldParams[name]
				if proposed.required && (baseline == nil || (!baseline.opaque && !baseline.required)) && !proposed.opaque {
					if strings.HasPrefix(proposed.location, "/parameters/path/") {
						work.unknown(&row, "path_parameter_added", proposed.location, "")
					} else {
						work.breaking(&row, "parameter_required", proposed.location+"/required")
					}
				}
				if baseline != nil && !baseline.opaque && !proposed.opaque {
					if baseline.style != proposed.style || baseline.explode != proposed.explode || baseline.allowReserved != proposed.allowReserved || baseline.allowEmpty != proposed.allowEmpty {
						work.unknown(&row, "parameter_serialization_changed", proposed.location, "")
					} else {
						work.compareSchema(&row, baseline.schema, proposed.schema, proposed.location+"/schema", 0)
					}
				}
			}
		}
		if work.exceeded {
			return RequestComparison{}, ErrRequestComparisonLimit
		}
		row.Status = "no_supported_breaks"
		if !row.Complete {
			row.Status = "unknown"
		}
		for _, finding := range row.Findings {
			if finding.Severity == "breaking" {
				row.Status = "breaking"
				break
			}
		}
		sort.Slice(row.Findings, func(i, j int) bool {
			a, b := row.Findings[i], row.Findings[j]
			return a.Location+" "+a.Severity+" "+a.Code+" "+a.Revision < b.Location+" "+b.Severity+" "+b.Code+" "+b.Revision
		})
		result.Routes = append(result.Routes, row)
	}
	return result, nil
}

func requestOperation(spec *Spec, path, method string) *Operation {
	if item := spec.Paths[path]; item != nil {
		return item.Methods[method]
	}
	return nil
}

func requestMetadata(value string) bool {
	return len(value) <= api.RequestCompatibilityMaxMetadataBytes && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func requestPointer(value string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(value)
}

func requestSortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type requestWork struct {
	nodes, bytes, findings int
	exceeded               bool
}

func (work *requestWork) use(count int) bool {
	work.nodes += count
	if work.nodes > api.RequestCompatibilityMaxNodes {
		work.exceeded = true
	}
	return !work.exceeded
}

func (work *requestWork) metadata(value string) bool {
	work.bytes += len(value)
	if work.bytes > api.RequestCompatibilityMaxWorkBytes {
		work.exceeded = true
	}
	return requestMetadata(value) && !work.exceeded
}

func (work *requestWork) emit(row *RequestRoute, severity, code, location, revision string) {
	if !requestMetadata(location) {
		code, location = "metadata_limit", ""
		severity = "unknown"
		row.Complete = false
	}
	finding := RequestFinding{Severity: severity, Code: code, Location: location, Revision: revision}
	for _, existing := range row.Findings {
		if existing == finding {
			return
		}
	}
	work.findings++
	if work.findings > api.RequestCompatibilityMaxFindings {
		work.exceeded = true
		return
	}
	row.Findings = append(row.Findings, finding)
}

func (work *requestWork) unknown(row *RequestRoute, code, location, revision string) {
	row.Complete = false
	work.emit(row, "unknown", code, location, revision)
}
func (work *requestWork) breaking(row *RequestRoute, code, location string) {
	work.emit(row, "breaking", code, location, "")
}

type requestParser struct {
	spec     *Spec
	work     *requestWork
	row      *RequestRoute
	revision string
}

func (parser *requestParser) unknown(code, location string) {
	parser.work.unknown(parser.row, code, location, parser.revision)
}

// Reference Objects and 3.0 schema refs ignore siblings. 3.1 schema refs with
// validation siblings require conjunction, which this adapter does not claim.
func (parser *requestParser) resolve(raw any, category, location string, depth int, refs map[string]bool) (any, bool) {
	if !parser.work.use(1) {
		return nil, false
	}
	if depth > api.RequestCompatibilityMaxDepth {
		parser.work.exceeded = true
		return nil, false
	}
	value, object := raw.(map[string]any)
	if !object {
		return raw, true
	}
	ref, present := value["$ref"]
	if !present {
		return raw, true
	}
	name, valid := ref.(string)
	if !valid || !parser.work.metadata(name) || !strings.HasPrefix(name, "#/components/"+category+"/") {
		parser.unknown("unresolved_local_reference", location)
		return nil, false
	}
	if category == "schemas" && strings.HasPrefix(parser.spec.version, "3.1.") {
		for key := range value {
			if key != "$ref" && !requestAnnotation(key) {
				parser.unknown("reference_siblings_not_compared", location)
				return nil, false
			}
		}
	}
	if refs[name] {
		parser.unknown("recursive_reference_not_compared", location)
		return nil, false
	}
	refs[name] = true
	defer delete(refs, name)
	var target any = parser.spec.Raw
	for _, part := range strings.Split(strings.TrimPrefix(name, "#/"), "/") {
		decoded, ok := requestDecodePointer(part)
		if !ok {
			parser.unknown("unresolved_local_reference", location)
			return nil, false
		}
		container, ok := target.(map[string]any)
		if !ok {
			parser.unknown("unresolved_local_reference", location)
			return nil, false
		}
		target, ok = container[decoded]
		if !ok {
			parser.unknown("unresolved_local_reference", location)
			return nil, false
		}
	}
	return parser.resolve(target, category, location, depth+1, refs)
}

func requestDecodePointer(value string) (string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == '~' {
			if i+1 == len(value) || (value[i+1] != '0' && value[i+1] != '1') {
				return "", false
			}
			i++
		}
	}
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(value), true
}

func requestAnnotation(key string) bool {
	switch key {
	case "title", "description", "example", "examples", "default", "deprecated", "writeOnly", "xml", "externalDocs":
		return true
	}
	return strings.HasPrefix(key, "x-")
}
