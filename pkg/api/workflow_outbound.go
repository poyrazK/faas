package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

const (
	WorkflowOutboundMaxQueryParameters = 32
	WorkflowOutboundMaxQueryValueBytes = 2048
	WorkflowOutboundMaxQueryBytes      = 4096
	WorkflowOutboundMaxPathValueBytes  = 512
	workflowOutboundResolveMaxBytes    = 16 << 10
)

// WorkflowOutboundSpec invokes an existing managed integration through outboundd.
// Path supports one workflow template per segment; Query values use the normal
// workflow templates. Credentials and the integration origin remain private.
type WorkflowOutboundSpec struct {
	IntegrationID        string            `json:"integration_id" yaml:"integration_id" toml:"integration_id"`
	Method               string            `json:"method" yaml:"method" toml:"method"`
	Path                 string            `json:"path" yaml:"path" toml:"path"`
	Query                map[string]string `json:"query,omitempty" yaml:"query,omitempty" toml:"query,omitempty"`
	IdempotencySupported bool              `json:"idempotency_supported,omitempty" yaml:"idempotency_supported,omitempty" toml:"idempotency_supported,omitempty"`

	// Runtime-only values. Path and Query contain the materialized request after
	// ResolveWorkflowOutboundTarget; these fields retain the immutable definition
	// used by outboundd to authorize the resolved request.
	PathTemplate  string            `json:"-" yaml:"-" toml:"-"`
	QueryTemplate map[string]string `json:"-" yaml:"-" toml:"-"`
	RawQuery      string            `json:"-" yaml:"-" toml:"-"`
}

func (s *WorkflowOutboundSpec) UnmarshalJSON(data []byte) error {
	type wire WorkflowOutboundSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var out wire
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*s = WorkflowOutboundSpec(out)
	return nil
}

func (s WorkflowOutboundSpec) SafeToRepeat() bool {
	return s.Method == "GET" || s.Method == "HEAD" || s.IdempotencySupported
}

var ErrWorkflowInvalidOutbound = errors.New("workflow: outbound requires a canonical integration UUID, allowed method, safe relative path template, bounded query parameters, and no outer method; mutating retries require idempotency_supported")

func ValidateWorkflowOutboundStep(step WorkflowStepSpec) error {
	s := step.Outbound
	if s == nil {
		return nil
	}
	if len(step.Name) == 0 || len(step.Name) > WorkflowOutboundStepNameMaxBytes {
		return ErrWorkflowInvalidOutbound
	}
	for i := range len(step.Name) {
		if step.Name[i] < 0x21 || step.Name[i] > 0x7e {
			return ErrWorkflowInvalidOutbound
		}
	}
	id, err := uuid.Parse(s.IntegrationID)
	if err != nil || id == uuid.Nil || id.String() != s.IntegrationID || step.Method != "" {
		return ErrWorkflowInvalidOutbound
	}
	policyPath, ok := WorkflowOutboundPathPolicySample(s.Path)
	if !ok || !routepolicy.CanonicalPath(policyPath) || !validWorkflowMethod(s.Method) || s.Method == "OPTIONS" || !workflowOutboundQueryValid(s.Query) {
		return ErrWorkflowInvalidOutbound
	}
	if (s.Method == "GET" || s.Method == "HEAD") && len(step.Input) != 0 {
		return ErrWorkflowInvalidOutbound
	}
	if int64(len(step.Input)) > WorkflowOutboundBodyMaxBytes {
		return ErrWorkflowInvalidOutbound
	}
	if step.Retry != nil && step.Retry.MaxAttempts > 1 && !s.SafeToRepeat() {
		return ErrWorkflowInvalidOutbound
	}
	return nil
}

// WorkflowOutboundPathPolicySample replaces each whole-segment template with a
// safe literal. Publication uses the sample for the integration/binding prefix
// check; the resolved path is checked again by outboundd at execution time.
func WorkflowOutboundPathPolicySample(path string) (string, bool) {
	if path == "" || len(path) > routepolicy.MaxRequestLength || path[0] != '/' {
		return "", false
	}
	segments := strings.Split(path, "/")
	for index := range segments {
		segment := segments[index]
		if !strings.ContainsAny(segment, "{}") {
			continue
		}
		if !strings.HasPrefix(segment, "{{") || !strings.HasSuffix(segment, "}}") || strings.Count(segment, "{{") != 1 || strings.Count(segment, "}}") != 1 || len(segment) <= 4 || strings.ContainsAny(segment[2:len(segment)-2], "{}") {
			return "", false
		}
		segments[index] = "x"
	}
	return strings.Join(segments, "/"), true
}

// WorkflowOutboundPathAllowedByPolicy checks both the sample route and that
// every permission-prefix segment needed for the match is fixed in the
// workflow definition. A sample placeholder must not make a denied template
// appear to fit a narrow route prefix.
func WorkflowOutboundPathAllowedByPolicy(method, path string, policy routepolicy.Policy) bool {
	sample, ok := WorkflowOutboundPathPolicySample(path)
	if !ok || !policy.AllowsRequest(method, sample) {
		return false
	}
	for _, prefix := range policy.AllowedPathPrefixes {
		if prefix != "/" && sample != prefix && !strings.HasPrefix(sample, prefix+"/") {
			continue
		}
		if prefix == "/" {
			return true
		}
		prefixSegments := strings.Split(strings.TrimPrefix(prefix, "/"), "/")
		templateSegments := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(templateSegments) < len(prefixSegments) {
			continue
		}
		fixed := true
		for index, segment := range prefixSegments {
			if templateSegments[index] != segment || strings.ContainsAny(templateSegments[index], "{}") {
				fixed = false
				break
			}
		}
		if fixed {
			return true
		}
	}
	return false
}

func workflowOutboundQueryValid(query map[string]string) bool {
	if len(query) > WorkflowOutboundMaxQueryParameters {
		return false
	}
	total := 0
	for key, value := range query {
		if key == "" || len(key) > 128 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "{}&=#?") || len(value) > WorkflowOutboundMaxQueryValueBytes {
			return false
		}
		for i := range len(key) {
			if key[i] < 0x21 || key[i] > 0x7e {
				return false
			}
		}
		total += len(key) + len(value)
		if total > WorkflowOutboundMaxQueryBytes {
			return false
		}
	}
	return true
}

func workflowOutboundInputReferences(spec *WorkflowOutboundSpec, stepNames []string) ([]workflowInputReference, error) {
	if spec == nil {
		return nil, nil
	}
	if !workflowOutboundQueryValid(spec.Query) {
		return nil, ErrWorkflowInvalidOutbound
	}
	if _, ok := WorkflowOutboundPathPolicySample(spec.Path); !ok {
		return nil, ErrWorkflowInvalidOutbound
	}
	template, err := json.Marshal(struct {
		Path  string            `json:"path"`
		Query map[string]string `json:"query"`
	}{Path: spec.Path, Query: spec.Query})
	if err != nil {
		return nil, ErrWorkflowInvalidOutbound
	}
	return workflowInputReferences(template, stepNames)
}

// ResolveWorkflowOutboundTarget resolves URL values using the same input and
// dependency templates as the request body. Dynamic path values are escaped as
// individual segments and may not introduce path separators or dot segments.
func ResolveWorkflowOutboundTarget(spec WorkflowOutboundSpec, input json.RawMessage, outputs map[string]json.RawMessage, failureContext json.RawMessage) (WorkflowOutboundSpec, error) {
	policyPath, ok := WorkflowOutboundPathPolicySample(spec.Path)
	if !ok || !routepolicy.CanonicalPath(policyPath) || !workflowOutboundQueryValid(spec.Query) {
		return WorkflowOutboundSpec{}, ErrWorkflowInvalidOutbound
	}
	resolved := spec
	resolved.PathTemplate = spec.Path
	resolved.QueryTemplate = cloneWorkflowStringMap(spec.Query)
	segments := strings.Split(spec.Path, "/")
	for index, segment := range segments {
		if !strings.HasPrefix(segment, "{{") {
			continue
		}
		value, err := resolveWorkflowOutboundScalar(segment, input, outputs, failureContext)
		if err != nil || len(value) == 0 || len(value) > WorkflowOutboundMaxPathValueBytes || strings.ContainsAny(value, "/\\%") || value == "." || value == ".." {
			return WorkflowOutboundSpec{}, ErrWorkflowInvalidOutbound
		}
		segments[index] = url.PathEscape(value)
	}
	resolved.Path = strings.Join(segments, "/")
	if !routepolicy.CanonicalPath(resolved.Path) {
		return WorkflowOutboundSpec{}, ErrWorkflowInvalidOutbound
	}
	query := make(url.Values, len(spec.Query))
	resolved.Query = make(map[string]string, len(spec.Query))
	for key, template := range spec.Query {
		value, err := resolveWorkflowOutboundScalar(template, input, outputs, failureContext)
		if err != nil || len(value) > WorkflowOutboundMaxQueryValueBytes {
			return WorkflowOutboundSpec{}, ErrWorkflowInvalidOutbound
		}
		resolved.Query[key] = value
		query.Set(key, value)
	}
	resolved.RawQuery = query.Encode()
	if len(resolved.RawQuery) > WorkflowOutboundMaxQueryBytes {
		return WorkflowOutboundSpec{}, ErrWorkflowInvalidOutbound
	}
	return resolved, nil
}

func resolveWorkflowOutboundScalar(template string, input json.RawMessage, outputs map[string]json.RawMessage, failureContext json.RawMessage) (string, error) {
	rawTemplate, err := json.Marshal(template)
	if err != nil {
		return "", err
	}
	resolved, err := ResolveWorkflowStepInputBounded(rawTemplate, input, outputs, failureContext, workflowOutboundResolveMaxBytes)
	if err != nil {
		return "", err
	}
	value, err := decodeWorkflowJSON(resolved)
	if err != nil || value == nil {
		return "", ErrWorkflowInvalidOutbound
	}
	text, err := workflowInputScalarString(value)
	if err != nil {
		return "", err
	}
	return text, nil
}

func cloneWorkflowStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// WorkflowOutboundRequestMatchesTemplate verifies that an outbound request has
// the same fixed route shape and query keys as its signed workflow definition.
func WorkflowOutboundRequestMatchesTemplate(pathTemplate, path string, queryTemplate map[string]string, rawQuery string) bool {
	policyPath, ok := WorkflowOutboundPathPolicySample(pathTemplate)
	if !ok || !routepolicy.CanonicalPath(policyPath) || !routepolicy.CanonicalPath(path) {
		return false
	}
	templateSegments := strings.Split(pathTemplate, "/")
	pathSegments := strings.Split(path, "/")
	if len(templateSegments) != len(pathSegments) {
		return false
	}
	for index, segment := range templateSegments {
		if strings.HasPrefix(segment, "{{") {
			decoded, err := url.PathUnescape(pathSegments[index])
			if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\%") || url.PathEscape(decoded) != pathSegments[index] {
				return false
			}
			continue
		}
		if segment != pathSegments[index] {
			return false
		}
	}
	if !workflowOutboundQueryValid(queryTemplate) {
		return false
	}
	actualQuery, err := url.ParseQuery(rawQuery)
	if err != nil || actualQuery.Encode() != rawQuery || len(actualQuery) != len(queryTemplate) {
		return false
	}
	for key, template := range queryTemplate {
		values, exists := actualQuery[key]
		if !exists || len(values) != 1 || !workflowOutboundTextMatches(template, values[0]) {
			return false
		}
	}
	return true
}

func workflowOutboundTextMatches(template, value string) bool {
	var pattern strings.Builder
	pattern.WriteByte('^')
	position := 0
	for {
		open := strings.Index(template[position:], "{{")
		if open < 0 {
			if strings.Contains(template[position:], "}}") {
				return false
			}
			pattern.WriteString(regexp.QuoteMeta(template[position:]))
			break
		}
		open += position
		close := strings.Index(template[open+2:], "}}")
		if close < 0 {
			return false
		}
		close += open + 2
		if strings.Contains(template[open+2:close], "{{") {
			return false
		}
		pattern.WriteString(regexp.QuoteMeta(template[position:open]))
		pattern.WriteString("(?s:.*?)")
		position = close + 2
	}
	pattern.WriteByte('$')
	matched, err := regexp.MatchString(pattern.String(), value)
	return err == nil && matched
}
