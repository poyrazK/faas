package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const testHTTPBodyLimit = 1 << 20

var testHTTPNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var testHTTPHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Requests run before wait_for; checks run after it. Captures are available to
// later steps in either list and to invocation wait conditions.
type testHTTPRequest struct {
	Name    string            `yaml:"name"`
	As      string            `yaml:"as"`
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
	JSON    any               `yaml:"json"`
	Expect  testHTTPExpect    `yaml:"expect"`
	Capture map[string]string `yaml:"capture"`
}

type testHTTPExpect struct {
	Status      int            `yaml:"status"`
	ContentType string         `yaml:"content_type,omitempty"`
	JSON        map[string]any `yaml:"json,omitempty"`
}

type testHTTPRequestEvidence struct {
	Name       string `json:"name"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

func validateTestHTTPRequests(scenario testScenario) error {
	return validateTestHTTPRequestsWithData(scenario, nil)
}

// Unselected scenarios may require a different data file. Still validate their
// reference syntax, without binding those fields to the selected scenario's rows.
func testHTTPDataFields(scenario testScenario) []string {
	fields := make(map[string]bool)
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case string:
			for _, match := range testSecretReferencePattern.FindAllString(value, -1) {
				key := match[2 : len(match)-1]
				if field, ok := strings.CutPrefix(key, "data."); ok {
					field = strings.TrimSuffix(field, ".string")
					if testTriggerKeyPattern.MatchString(field) {
						fields[field] = true
					}
				}
			}
		case map[string]any:
			for _, item := range value {
				collect(item)
			}
		case []any:
			for _, item := range value {
				collect(item)
			}
		}
	}
	steps := append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...)
	for _, scenarioStep := range scenario.Steps {
		steps = append(steps, scenarioStep.Requests...)
		steps = append(steps, scenarioStep.Checks...)
	}
	for _, step := range steps {
		collect(step.Path)
		for _, value := range step.Headers {
			collect(value)
		}
		collect(step.JSON)
		collect(step.Expect.JSON)
	}
	result := make([]string, 0, len(fields))
	for field := range fields {
		result = append(result, field)
	}
	return result
}

func validateTestHTTPRequestsWithData(scenario testScenario, dataFields []string) error {
	if len(scenario.Requests)+len(scenario.Checks) > 100 {
		return fmt.Errorf("at most 100 HTTP steps may be declared")
	}
	consumers := make(map[string]bool, len(scenario.Consumers))
	for _, consumer := range scenario.Consumers {
		consumers[consumer.Name] = true
	}
	known := map[string]string{"run.id": "example-run-id"}
	for _, field := range dataFields {
		known["data."+field] = "example-value"
		known["data."+field+".string"] = "example-value"
	}
	names := make(map[string]bool)
	captureNames := make(map[string]bool)
	beforeWaitCaptures := make(map[string]bool)
	for _, step := range append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...) {
		beforeWait := false
		for _, request := range scenario.Requests {
			if request.Name == step.Name {
				beforeWait = true
				break
			}
		}
		if !testHTTPNamePattern.MatchString(step.Name) || names[step.Name] {
			return fmt.Errorf("HTTP step has invalid or duplicate name %q", step.Name)
		}
		names[step.Name] = true
		if step.As != "" && !consumers[step.As] {
			return fmt.Errorf("HTTP step %q names undeclared consumer %q", step.Name, step.As)
		}
		if step.Method != http.MethodGet && step.Method != http.MethodHead && step.Method != http.MethodPost && step.Method != http.MethodPut && step.Method != http.MethodPatch && step.Method != http.MethodDelete {
			return fmt.Errorf("HTTP step %q has unsupported method %q", step.Name, step.Method)
		}
		if step.Expect.Status < 100 || step.Expect.Status > 599 {
			return fmt.Errorf("HTTP step %q needs expect.status between 100 and 599", step.Name)
		}
		if step.JSON != nil && (step.Method == http.MethodGet || step.Method == http.MethodHead) {
			return fmt.Errorf("HTTP step %q cannot send JSON with %s", step.Name, step.Method)
		}
		if step.Expect.ContentType != "" {
			if _, _, err := mime.ParseMediaType(step.Expect.ContentType); err != nil {
				return fmt.Errorf("HTTP step %q has invalid expected content type: %w", step.Name, err)
			}
		}
		if _, err := expandTestHTTPPath(step.Path, known); err != nil {
			return fmt.Errorf("HTTP step %q path: %w", step.Name, err)
		}
		for key, value := range step.Headers {
			if !testHTTPHeaderPattern.MatchString(key) || strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Proxy-Authorization") {
				return fmt.Errorf("HTTP step %q has unsupported header %q", step.Name, key)
			}
			if step.As != "" && strings.EqualFold(key, "Authorization") {
				return fmt.Errorf("HTTP step %q cannot set Authorization with as", step.Name)
			}
			if _, err := expandTestHTTPTemplate(value, known, nil); err != nil {
				return fmt.Errorf("HTTP step %q header %q: %w", step.Name, key, err)
			}
		}
		if _, err := expandTestHTTPJSON(step.JSON, known); err != nil {
			return fmt.Errorf("HTTP step %q JSON: %w", step.Name, err)
		}
		for pointer, expected := range step.Expect.JSON {
			if _, err := testJSONPointerParts(pointer); err != nil {
				return fmt.Errorf("HTTP step %q expectation: %w", step.Name, err)
			}
			if _, err := expandTestHTTPJSON(expected, known); err != nil {
				return fmt.Errorf("HTTP step %q expectation %q: %w", step.Name, pointer, err)
			}
		}
		for capture, pointer := range step.Capture {
			if !testTriggerKeyPattern.MatchString(capture) || captureNames[capture] {
				return fmt.Errorf("HTTP step %q has invalid or duplicate capture %q", step.Name, capture)
			}
			if _, err := testJSONPointerParts(pointer); err != nil {
				return fmt.Errorf("HTTP step %q capture %q: %w", step.Name, capture, err)
			}
			captureNames[capture] = true
			known["steps."+step.Name+"."+capture] = "example-value"
			if beforeWait {
				beforeWaitCaptures[capture] = true
			}
		}
	}
	if len(scenario.Trigger) == 0 {
		for _, condition := range scenario.WaitFor.Invocations {
			if !beforeWaitCaptures[condition.TriggerKey] {
				return fmt.Errorf("invocation trigger_key %q must be captured by a request", condition.TriggerKey)
			}
		}
	}
	return nil
}

func expandTestHTTPTemplate(input string, values map[string]string, escape func(string) string) (string, error) {
	if strings.Contains(testSecretReferencePattern.ReplaceAllString(input, ""), "${") {
		return "", fmt.Errorf("invalid template reference")
	}
	var expansionErr error
	result := testSecretReferencePattern.ReplaceAllStringFunc(input, func(match string) string {
		key := match[2 : len(match)-1]
		value, ok := values[key]
		if !ok {
			expansionErr = fmt.Errorf("unknown reference %s", match)
			return match
		}
		if escape != nil {
			return escape(value)
		}
		return value
	})
	if expansionErr != nil {
		return "", expansionErr
	}
	return result, nil
}

func expandTestHTTPPath(path string, values map[string]string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "#") || len(path) > 4096 {
		return "", fmt.Errorf("path must be an app-relative path starting with one slash, without a fragment")
	}
	pathPart, queryPart, hasQuery := strings.Cut(path, "?")
	pathPart, err := expandTestHTTPTemplate(pathPart, values, url.PathEscape)
	if err != nil {
		return "", err
	}
	if hasQuery {
		queryPart, err = expandTestHTTPTemplate(queryPart, values, url.QueryEscape)
		if err != nil {
			return "", err
		}
		pathPart += "?" + queryPart
	}
	parsed, err := url.ParseRequestURI(pathPart)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf("invalid app-relative path")
	}
	return pathPart, nil
}

func expandTestHTTPJSON(value any, values map[string]string) (any, error) {
	return expandTestHTTPJSONWithData(value, values, nil)
}

func expandTestHTTPJSONWithData(value any, values map[string]string, data map[string]any) (any, error) {
	switch value := value.(type) {
	case string:
		// An exact data reference keeps JSON numbers and booleans typed. A
		// reference embedded in other text remains string interpolation.
		if strings.HasPrefix(value, "${data.") && strings.HasSuffix(value, "}") {
			if actual, exists := data[value[7:len(value)-1]]; exists {
				return actual, nil
			}
		}
		return expandTestHTTPTemplate(value, values, nil)
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			expanded, err := expandTestHTTPJSONWithData(item, values, data)
			if err != nil {
				return nil, err
			}
			result[key] = expanded
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			expanded, err := expandTestHTTPJSONWithData(item, values, data)
			if err != nil {
				return nil, err
			}
			result[i] = expanded
		}
		return result, nil
	default:
		return value, nil
	}
}

func runTestHTTPRequests(ctx context.Context, baseURL, runID string, consumerEnv []string, steps []testHTTPRequest, captures map[string]string) ([]testHTTPRequestEvidence, error) {
	return runTestHTTPRequestsWithData(ctx, baseURL, runID, consumerEnv, steps, captures, nil)
}

func runTestHTTPRequestsWithData(ctx context.Context, baseURL, runID string, consumerEnv []string, steps []testHTTPRequest, captures map[string]string, data map[string]any) ([]testHTTPRequestEvidence, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	values := testHTTPValues(runID, captures, data)
	consumerKeys := testHTTPConsumerKeys(consumerEnv)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	evidence := make([]testHTTPRequestEvidence, 0, len(steps))
	for _, step := range steps {
		result := testHTTPRequestEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0]}
		started := time.Now()
		err := runOneTestHTTPRequest(ctx, client, baseURL, step, values, consumerKeys, captures, data, &result)
		result.DurationMS = time.Since(started).Milliseconds()
		result.Passed = err == nil
		if err != nil {
			result.Error = err.Error()
		}
		evidence = append(evidence, result)
		if err != nil {
			return evidence, fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return evidence, nil
}

func testHTTPValues(runID string, captures map[string]string, data map[string]any) map[string]string {
	values := make(map[string]string, len(captures)+1)
	values["run.id"] = runID
	for key, value := range data {
		values["data."+key] = fmt.Sprint(value)
		values["data."+key+".string"] = fmt.Sprint(value)
	}
	for key, value := range captures {
		if strings.HasPrefix(key, "steps.") {
			values[key] = value
		}
	}
	return values
}

func testHTTPConsumerKeys(consumerEnv []string) map[string]string {
	consumerKeys := make(map[string]string)
	for _, entry := range consumerEnv {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "GREGALE_TEST_CONSUMER_") && strings.HasSuffix(key, "_KEY") {
			consumerKeys[strings.TrimSuffix(strings.TrimPrefix(key, "GREGALE_TEST_CONSUMER_"), "_KEY")] = value
		}
	}
	return consumerKeys
}

func runOneTestHTTPRequest(ctx context.Context, client *http.Client, baseURL string, step testHTTPRequest, values, consumerKeys, captures map[string]string, data map[string]any, evidence *testHTTPRequestEvidence) error {
	return runOneTestHTTPRequestWithBody(ctx, client, baseURL, step, values, consumerKeys, captures, data, evidence, false)
}

// Load timings include bounded response consumption, even for status-only steps.
// This also allows the load transport to reuse connections between journeys.
func runOneTestHTTPRequestWithBody(ctx context.Context, client *http.Client, baseURL string, step testHTTPRequest, values, consumerKeys, captures map[string]string, data map[string]any, evidence *testHTTPRequestEvidence, fullBody bool) error {
	path, err := expandTestHTTPPath(step.Path, values)
	if err != nil {
		return err
	}
	var body io.Reader
	if step.JSON != nil {
		expanded, err := expandTestHTTPJSONWithData(step.JSON, values, data)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(expanded)
		if err != nil {
			return fmt.Errorf("encode request JSON: %w", err)
		}
		if len(encoded) > testHTTPBodyLimit {
			return fmt.Errorf("request JSON exceeds 1 MiB")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, step.Method, baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: invalid path or body")
	}
	if step.JSON != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range step.Headers {
		expanded, err := expandTestHTTPTemplate(value, values, nil)
		if err != nil {
			return fmt.Errorf("header %s: %w", key, err)
		}
		request.Header.Set(key, expanded)
	}
	if step.As != "" {
		key := strings.ToUpper(strings.ReplaceAll(step.As, "-", "_"))
		token := consumerKeys[key]
		if token == "" {
			return fmt.Errorf("consumer %q has no test key", step.As)
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			err = urlError.Err
		}
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	evidence.Status = response.StatusCode
	var responseBody []byte
	if fullBody {
		responseBody, err = readTestHTTPResponse(response.Body)
		if err != nil {
			return err
		}
	}
	if response.StatusCode != step.Expect.Status {
		return fmt.Errorf("expected status %d, received %d", step.Expect.Status, response.StatusCode)
	}
	if step.Expect.ContentType != "" {
		actual, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		expected, _, _ := mime.ParseMediaType(step.Expect.ContentType)
		if err != nil || actual != expected {
			return fmt.Errorf("expected content type %s", step.Expect.ContentType)
		}
	}
	if len(step.Expect.JSON) == 0 && len(step.Capture) == 0 {
		return nil
	}
	if !fullBody {
		responseBody, err = readTestHTTPResponse(response.Body)
		if err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode response JSON: %w", err)
	}
	for pointer, expected := range step.Expect.JSON {
		actual, ok := testJSONPointer(document, pointer)
		if !ok {
			return fmt.Errorf("expected JSON pointer %q is missing", pointer)
		}
		expanded, err := expandTestHTTPJSONWithData(expected, values, data)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(expanded)
		if err != nil {
			return fmt.Errorf("encode expected JSON: %w", err)
		}
		comparator := json.NewDecoder(bytes.NewReader(encoded))
		comparator.UseNumber()
		var normalized any
		if err := comparator.Decode(&normalized); err != nil || !reflect.DeepEqual(actual, normalized) {
			return fmt.Errorf("JSON pointer %q did not match", pointer)
		}
	}
	for name, pointer := range step.Capture {
		actual, ok := testJSONPointer(document, pointer)
		if !ok {
			return fmt.Errorf("capture %q JSON pointer %q is missing", name, pointer)
		}
		var value string
		switch actual := actual.(type) {
		case string:
			value = actual
		case json.Number:
			value = string(actual)
		case bool:
			value = strconv.FormatBool(actual)
		default:
			return fmt.Errorf("capture %q must select a string, number, or boolean", name)
		}
		captures[name] = value
		captures["steps."+step.Name+"."+name] = value
		values["steps."+step.Name+"."+name] = value
	}
	return nil
}

func readTestHTTPResponse(body io.Reader) ([]byte, error) {
	responseBody, err := io.ReadAll(io.LimitReader(body, testHTTPBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > testHTTPBodyLimit {
		return nil, errors.New("response body exceeds 1 MiB")
	}
	return responseBody, nil
}

func testJSONPointerParts(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON pointer %q must start with /", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		for j := 0; j < len(part); j++ {
			if part[j] == '~' && (j+1 == len(part) || (part[j+1] != '0' && part[j+1] != '1')) {
				return nil, fmt.Errorf("JSON pointer %q has invalid escape", pointer)
			}
			if part[j] == '~' {
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func testJSONPointer(document any, pointer string) (any, bool) {
	parts, err := testJSONPointerParts(pointer)
	if err != nil {
		return nil, false
	}
	for _, part := range parts {
		switch current := document.(type) {
		case map[string]any:
			value, ok := current[part]
			if !ok {
				return nil, false
			}
			document = value
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) {
				return nil, false
			}
			document = current[index]
		default:
			return nil, false
		}
	}
	return document, true
}
