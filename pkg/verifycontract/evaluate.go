package verifycontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Check outcomes.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
	// StatusError means the check could not produce a verdict: the request
	// did not reach the candidate, the database was not found, the budget
	// ran out. A required check in error blocks promotion like a failure.
	StatusError = "error"
)

// Stable codes a CheckResult carries when it did not pass.
const (
	CodeAssertionFailed      = "verify_assertion_failed"
	CodeRequestFailed        = "verify_request_failed"
	CodeTimeout              = "verify_timeout"
	CodeMutationInProduction = "verify_mutation_skipped_in_production"
	CodeMCPFailed            = "verify_mcp_failed"
	CodeMCPToolNotReadOnly   = "verify_mcp_tool_not_read_only"
	CodeDatabaseNotFound     = "verify_database_not_found"
	CodeQueryFailed          = "verify_query_failed"
	CodeQueryUnavailable     = "verify_query_unavailable"
	CodeNotRun               = "verify_not_run"
	// CodeSecretMissing: a header references a secret the deployment's
	// scope does not hold (amendment 2).
	CodeSecretMissing = "verify_secret_missing"
)

// Environments a report records.
const (
	EnvironmentProduction    = "production"
	EnvironmentNonProduction = "non_production"
)

// Report is the one pass/fail document a verification produces. It is
// stored on the deployment's hosting receipt and returned by deploy --json.
type Report struct {
	Version        int           `json:"version"`
	Status         string        `json:"status"`
	ContractSHA256 string        `json:"contract_sha256"`
	Environment    string        `json:"environment"`
	DurationMS     int64         `json:"duration_ms"`
	Checks         []CheckResult `json:"checks"`
}

// CheckResult is one check's outcome. Failures say which assertion failed;
// they quote numbers, booleans and status codes, never response text or row
// values, because the report outlives the deployment.
type CheckResult struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Required   bool     `json:"required"`
	Status     string   `json:"status"`
	Code       string   `json:"code,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	HTTPStatus int      `json:"http_status,omitempty"`
	Failures   []string `json:"failures,omitempty"`
}

// Finish computes the report's status: failed when any required check did
// not pass or get skipped.
func (r *Report) Finish() {
	r.Version = Version
	r.Status = StatusPassed
	for _, c := range r.Checks {
		if c.Required && (c.Status == StatusFailed || c.Status == StatusError) {
			r.Status = StatusFailed
			return
		}
	}
}

// Blocking lists the names of required checks that failed or errored.
func (r Report) Blocking() []string {
	var names []string
	for _, c := range r.Checks {
		if c.Required && (c.Status == StatusFailed || c.Status == StatusError) {
			names = append(names, c.Name)
		}
	}
	return names
}

// HTTPResponse is what the runner observed for one HTTP check.
type HTTPResponse struct {
	StatusCode    int
	Header        http.Header
	Body          []byte
	BodyTruncated bool
	Latency       time.Duration
}

// EvaluateHTTP returns the failed assertions; none means the check passed.
func EvaluateHTTP(expect HTTPExpect, resp HTTPResponse) []string {
	var failures []string
	if len(expect.Status) == 0 {
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			failures = append(failures, fmt.Sprintf("status %d is not 2xx", resp.StatusCode))
		}
	} else if !slices.Contains(expect.Status, resp.StatusCode) {
		failures = append(failures, fmt.Sprintf("status %d is not one of %v", resp.StatusCode, []int(expect.Status)))
	}
	if expect.MaxLatencyMS > 0 && resp.Latency > time.Duration(expect.MaxLatencyMS)*time.Millisecond {
		failures = append(failures, fmt.Sprintf("latency %d ms exceeds %d ms", resp.Latency.Milliseconds(), expect.MaxLatencyMS))
	}
	for _, name := range sortedKeys(expect.Headers) {
		values := resp.Header.Values(name)
		found := false
		for _, v := range values {
			if strings.Contains(v, expect.Headers[name]) {
				found = true
				break
			}
		}
		if !found {
			if len(values) == 0 {
				failures = append(failures, fmt.Sprintf("header %s is missing", http.CanonicalHeaderKey(name)))
			} else {
				failures = append(failures, fmt.Sprintf("header %s does not contain the expected value", http.CanonicalHeaderKey(name)))
			}
		}
	}
	for i, s := range expect.BodyContains {
		if !bytes.Contains(resp.Body, []byte(s)) {
			failures = append(failures, fmt.Sprintf("body does not contain body_contains[%d]", i))
		}
	}
	if len(expect.JSON) > 0 {
		failures = append(failures, evaluateJSON(expect.JSON, resp)...)
	}
	return failures
}

func evaluateJSON(assertions map[string]any, resp HTTPResponse) []string {
	if resp.BodyTruncated {
		return []string{fmt.Sprintf("body exceeds %d bytes; JSON assertions cannot be checked", MaxResponseBytes)}
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(resp.Body))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return []string{"body is not JSON"}
	}
	var failures []string
	for _, pointer := range sortedKeys(assertions) {
		tokens, _ := parsePointer(pointer)
		actual, ok := resolvePointer(document, tokens)
		if !ok {
			failures = append(failures, fmt.Sprintf("json %q is missing", pointer))
			continue
		}
		if !JSONEqual(assertions[pointer], actual) {
			failures = append(failures, fmt.Sprintf("json %q %s", pointer, mismatch(actual)))
		}
	}
	return failures
}

// mismatch describes an unexpected value without quoting app text: numbers,
// booleans and null are safe to show, strings and containers are not.
func mismatch(actual any) string {
	switch v := actual.(type) {
	case json.Number:
		return "is " + v.String() + ", not the expected value"
	case float64:
		return "is " + strconv.FormatFloat(v, 'g', -1, 64) + ", not the expected value"
	case bool:
		return "is " + strconv.FormatBool(v) + ", not the expected value"
	case nil:
		return "is null, not the expected value"
	case string:
		return "is a different string"
	case []any:
		return fmt.Sprintf("is a different array (length %d)", len(v))
	case map[string]any:
		return "is a different object"
	}
	return "does not match"
}

// JSONEqual compares two JSON values by their canonical encoding, so 1 and
// 1.0 are equal and key order does not matter.
func JSONEqual(expected, actual any) bool {
	a, err := canonicalJSON(expected)
	if err != nil {
		return false
	}
	b, err := canonicalJSON(actual)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func canonicalJSON(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") || len(pointer) > 512 {
		return nil, errors.New(`a JSON pointer is "" or starts with "/"`)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func resolvePointer(document any, tokens []string) (any, bool) {
	current := document
	for _, token := range tokens {
		switch v := current.(type) {
		case map[string]any:
			next, ok := v[token]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(v) || (len(token) > 1 && token[0] == '0') {
				return nil, false
			}
			current = v[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// EvaluateSQL checks a read-only query result: rowCount rows were read and
// first is the first row's first column (nil for NULL or no rows).
func EvaluateSQL(expect SQLExpect, rowCount int, hasFirst bool, first *string) []string {
	var failures []string
	if expect.Rows != nil && rowCount != *expect.Rows {
		failures = append(failures, fmt.Sprintf("query returned %d rows, expected %d", rowCount, *expect.Rows))
	}
	if expect.Null {
		if !hasFirst {
			failures = append(failures, "query returned no rows, expected a NULL value")
		} else if first != nil {
			failures = append(failures, "value is not NULL")
		}
	}
	if expect.Value != nil {
		switch {
		case !hasFirst:
			failures = append(failures, "query returned no rows, expected a value")
		case first == nil:
			failures = append(failures, "value is NULL, not the expected value")
		case *first != *expect.Value:
			failures = append(failures, "value "+describeSQLValue(*first)+", not the expected value")
		}
	}
	return failures
}

// describeSQLValue shows a numeric or boolean value (a count, a sum, an
// exists flag) and hides any other text.
func describeSQLValue(v string) string {
	if _, err := strconv.ParseFloat(v, 64); err == nil && len(v) <= 32 {
		return "is " + v
	}
	if v == "t" || v == "f" || v == "true" || v == "false" {
		return "is " + v
	}
	return "differs"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
