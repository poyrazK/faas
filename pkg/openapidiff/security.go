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

// SecurityComparison compares captured declarations, not runtime enforcement
// or the relative strength of credential mechanisms. Private names, scopes,
// URLs, references, and extensions never appear in its output.
type SecurityComparison struct {
	Routes []SecurityRoute `json:"routes"`
}

type SecurityRoute struct {
	Method         string           `json:"method"`
	Path           string           `json:"path"`
	Status         string           `json:"status"`
	Complete       bool             `json:"complete"`
	Changed        bool             `json:"changed"`
	Regression     bool             `json:"regression"`
	ClientBreaking bool             `json:"client_breaking"`
	Baseline       *SecuritySummary `json:"baseline,omitempty"`
	Candidate      *SecuritySummary `json:"candidate,omitempty"`
	Findings       []RequestFinding `json:"findings"`
}

type SecuritySummary struct {
	Source          string   `json:"source"`
	Authentication  string   `json:"authentication"`
	CredentialKinds []string `json:"credential_kinds"`
}

var ErrSecurityComparisonLimit = errors.New("security comparison exceeds its work or output limit")

type securityClause map[string]map[string]bool
type securityPolicy struct {
	clauses  []securityClause
	schemes  map[string]securityScheme
	complete bool
	summary  SecuritySummary
}
type securityScheme struct {
	kind     string
	identity string
	scopes   map[string]bool
}
type securityWork struct {
	nodes, bytes, findings int
	exceeded               bool
}

func (work *securityWork) use(count int) bool {
	work.nodes += count
	work.exceeded = work.exceeded || work.nodes > api.SecurityCompatibilityMaxNodes
	return !work.exceeded
}
func (work *securityWork) metadata(value string) bool {
	work.bytes += len(value)
	work.exceeded = work.exceeded || work.bytes > api.SecurityCompatibilityMaxWorkBytes
	return !work.exceeded && len(value) <= api.SecurityCompatibilityMaxMetadataBytes && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
func (work *securityWork) emit(row *SecurityRoute, severity, code, revision string) {
	finding := RequestFinding{Severity: severity, Code: code, Location: "/security", Revision: revision}
	for _, existing := range row.Findings {
		if existing == finding {
			return
		}
	}
	work.findings++
	if work.findings > api.SecurityCompatibilityMaxFindings {
		work.exceeded = true
		return
	}
	row.Findings = append(row.Findings, finding)
	if severity == "unknown" {
		row.Complete = false
	}
}

// CompareSecurity evaluates OR alternatives of AND credential requirements.
// Strict widening is a declared protection regression; strict narrowing breaks
// previously supported credential combinations. Incomparable replacements and
// changed scheme definitions require review instead of a strength ranking.
func CompareSecurity(base, candidate *Spec) (SecurityComparison, error) {
	work := &securityWork{}
	keys, err := securityRouteKeys(base, candidate, work)
	if err != nil {
		return SecurityComparison{}, err
	}
	result := SecurityComparison{Routes: []SecurityRoute{}}
	for _, key := range keys {
		path, method, _ := strings.Cut(key, "\x00")
		row := SecurityRoute{Method: strings.ToUpper(method), Path: path, Status: "not_compared", Findings: []RequestFinding{}}
		before, after := requestOperation(base, path, method), requestOperation(candidate, path, method)
		if before != nil && after != nil {
			row.Complete = true
			if !work.metadata(path) || !strings.HasPrefix(path, "/") {
				work.emit(&row, "unknown", "invalid_route_metadata", "")
			} else if !securityVersion(base) || !securityVersion(candidate) {
				work.emit(&row, "unknown", "unsupported_openapi_version", "")
			} else {
				old := securityParser{spec: base, work: work, row: &row, revision: "base"}
				new := securityParser{spec: candidate, work: work, row: &row, revision: "candidate"}
				baseline, proposed := old.policy(before), new.policy(after)
				row.Baseline, row.Candidate = &baseline.summary, &proposed.summary
				work.compare(&row, baseline, proposed)
			}
			row.Status = "unchanged"
			switch {
			case row.Regression:
				row.Status = "regression"
			case row.ClientBreaking:
				row.Status = "client_breaking"
			case !row.Complete:
				row.Status = "unknown"
			}
		}
		if work.exceeded {
			return SecurityComparison{}, ErrSecurityComparisonLimit
		}
		sort.Slice(row.Findings, func(i, j int) bool {
			a, b := row.Findings[i], row.Findings[j]
			return a.Code+" "+a.Revision < b.Code+" "+b.Revision
		})
		result.Routes = append(result.Routes, row)
	}
	return result, nil
}

func securityVersion(spec *Spec) bool {
	return strings.HasPrefix(spec.version, "3.0.") || strings.HasPrefix(spec.version, "3.1.")
}

func securityRouteKeys(base, candidate *Spec, work *securityWork) ([]string, error) {
	if base == nil || candidate == nil {
		return nil, errors.New("security comparison requires both captured documents")
	}
	keys := map[string]bool{}
	for _, spec := range []*Spec{base, candidate} {
		for path, item := range spec.Paths {
			if !work.use(1) {
				return nil, ErrSecurityComparisonLimit
			}
			if item == nil {
				continue
			}
			if _, found := item.Raw["$ref"]; found {
				return nil, errors.New("security comparison cannot resolve referenced path items")
			}
			for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"} {
				if _, declared := item.Raw[method]; declared && item.Methods[method] == nil {
					return nil, errors.New("security comparison requires parsed HTTP operations")
				}
			}
			for method := range item.Methods {
				if !work.use(1) {
					return nil, ErrSecurityComparisonLimit
				}
				keys[path+"\x00"+method] = true
				if len(keys) > api.SecurityCompatibilityMaxRoutes {
					return nil, ErrSecurityComparisonLimit
				}
			}
		}
	}
	return requestSortedKeys(keys), nil
}

func securityAnonymous(policy securityPolicy) bool {
	for _, clause := range policy.clauses {
		if len(clause) == 0 {
			return true
		}
	}
	return false
}

func (work *securityWork) compare(row *SecurityRoute, base, candidate securityPolicy) {
	row.Changed = !reflect.DeepEqual(base.clauses, candidate.clauses) || !reflect.DeepEqual(base.schemes, candidate.schemes)
	definitionsChanged := false
	for _, name := range requestSortedKeys(base.schemes) {
		old := base.schemes[name]
		if new, found := candidate.schemes[name]; found && old.identity != new.identity {
			definitionsChanged = true
			work.emit(row, "unknown", "security_scheme_changed", "")
		}
	}
	// A valid anonymous alternative proves removal independently of other
	// unsupported candidate alternatives; a complete baseline is still required.
	if base.complete && !securityAnonymous(base) && securityAnonymous(candidate) {
		row.Regression, row.Changed = true, true
		work.emit(row, "regression", "anonymous_access_added", "")
		return
	}
	if !base.complete || !candidate.complete || definitionsChanged {
		return
	}
	oldCovered := work.covered(base.clauses, candidate.clauses)
	newCovered := work.covered(candidate.clauses, base.clauses)
	row.Changed = !oldCovered || !newCovered
	switch {
	case oldCovered && newCovered:
		return
	case oldCovered:
		row.Regression = true
		work.emit(row, "regression", "security_requirements_weakened", "")
	case newCovered:
		row.ClientBreaking = true
		code := "security_requirements_restricted"
		if securityAnonymous(base) {
			code = "authentication_required"
		}
		work.emit(row, "client_breaking", code, "")
	default:
		work.emit(row, "unknown", "security_requirements_incomparable", "")
	}
}

// Every credential set satisfying a source conjunction must satisfy at least
// one target conjunction. Positive requirements make this exact: a conjunction
// implies an OR iff it implies at least one branch of that OR.
func (work *securityWork) covered(source, target []securityClause) bool {
	for _, clause := range source {
		found := false
		for _, alternative := range target {
			if !work.use(1) {
				return false
			}
			if work.implies(clause, alternative) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (work *securityWork) implies(source, target securityClause) bool {
	for name, required := range target {
		if !work.use(1) {
			return false
		}
		provided, present := source[name]
		if !present {
			return false
		}
		for scope := range required {
			if !work.use(1) || !provided[scope] {
				return false
			}
		}
	}
	return true
}
