package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// Diagnostic bounds protect the fixture runner, rather than product quotas.
const smokeMaxBytes = 1 << 20
const smokeMaxCases = 64

// SmokeSuite contains only reviewed arguments and expectations, never credentials.
type SmokeSuite struct {
	Version int         `json:"version"`
	Cases   []SmokeCase `json:"cases"`
}
type SmokeCase struct {
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Target          string            `json:"target,omitempty"`
	Arguments       map[string]any    `json:"arguments,omitempty"`
	PromptArguments map[string]string `json:"prompt_arguments,omitempty"`
	ExpectedResult  json.RawMessage   `json:"expected_result,omitempty"`
	MIMEType        string            `json:"mime_type,omitempty"`
	MessageRoles    []string          `json:"message_roles,omitempty"`
	Credential      string            `json:"credential,omitempty"`
	ExpectedStatus  int               `json:"expected_status,omitempty"`
}
type SmokeReport struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

func LoadSmokeSuite(r io.Reader) (SmokeSuite, error) {
	body, err := io.ReadAll(io.LimitReader(r, smokeMaxBytes+1))
	if err != nil || len(body) > smokeMaxBytes {
		return SmokeSuite{}, errors.New("could not read fixture file within 1 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var suite SmokeSuite
	if decoder.Decode(&suite) != nil {
		return suite, errors.New("invalid fixture JSON or unknown fields")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return suite, errors.New("fixtures must contain one JSON object")
	}
	return suite, suite.Validate()
}
func (s SmokeSuite) Validate() error {
	if s.Version != 1 || len(s.Cases) == 0 || len(s.Cases) > smokeMaxCases {
		return errors.New("fixture version must be 1 with 1 to 64 cases")
	}
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || strings.ContainsAny(c.Name, "\r\n\t") || seen[c.Name] {
			return errors.New("fixture names must be unique nonempty labels up to 128 bytes")
		}
		seen[c.Name] = true
		if len(c.ExpectedResult) > 0 && (!json.Valid(c.ExpectedResult) || bytes.Equal(bytes.TrimSpace(c.ExpectedResult), []byte("null"))) {
			return errors.New("expected_result must be valid non-null JSON")
		}
		switch c.Kind {
		case "tool":
			if c.Target == "" || len(c.ExpectedResult) == 0 || c.PromptArguments != nil || c.MIMEType != "" || c.MessageRoles != nil {
				return errors.New("tool fixtures require target and expected_result")
			}
		case "resource":
			if c.Target == "" || c.MIMEType == "" || c.Arguments != nil || c.PromptArguments != nil || c.MessageRoles != nil {
				return errors.New("resource fixtures require target URI and mime_type")
			}
		case "prompt":
			if c.Target == "" || len(c.MessageRoles) == 0 || c.Arguments != nil || c.MIMEType != "" {
				return errors.New("prompt fixtures require target and message_roles")
			}
			for _, role := range c.MessageRoles {
				if role != "user" && role != "assistant" {
					return errors.New("prompt message roles must be user or assistant")
				}
			}
		case "auth":
			if (c.Credential != "missing" && c.Credential != "malformed") || c.ExpectedStatus != 401 || c.Target != "" || c.Arguments != nil || c.PromptArguments != nil || c.MIMEType != "" || c.MessageRoles != nil || len(c.ExpectedResult) > 0 {
				return errors.New("auth fixtures require missing or malformed credential and expected_status 401")
			}
		default:
			return errors.New("unknown fixture kind")
		}
		if c.Kind != "auth" && (c.Credential != "" || c.ExpectedStatus != 0) {
			return errors.New("authentication fields only belong to auth fixtures")
		}
	}
	return nil
}
func (s SmokeSuite) RequiresToolExecution() bool {
	for _, c := range s.Cases {
		if c.Kind == "tool" {
			return true
		}
	}
	return false
}

// RunSmoke executes each reviewed fixture once. It never retries calls or follows
// Tasks/input requests. Diagnostics omit arguments, results and remote errors.
func RunSmoke(ctx context.Context, client *Client, suite SmokeSuite, allowTools bool) SmokeReport {
	report := SmokeReport{Checks: []Check{}}
	if suite.Validate() != nil || (suite.RequiresToolExecution() && !allowTools) {
		report.Checks = append(report.Checks, Check{Name: "fixture_configuration", Status: "failed", Detail: "Invalid fixtures or tool execution lacks explicit --allow-tool-execution."})
		return report
	}
	validation := ValidateCompatibility(ctx, client, nil)
	if !validation.OK {
		report.Checks = validation.Checks
		return report
	}
	report.OK = true
	for _, fixture := range suite.Cases {
		check := Check{Name: fixture.Name, Status: "passed"}
		if ctx.Err() != nil {
			check.Status = "failed"
			check.Detail = "Suite deadline reached; fixture was not executed."
		} else if !runSmokeCase(ctx, client, validation.Catalog, fixture) {
			check.Status = "failed"
			check.Detail = "Operation or expected result failed; inspect the fixture and server privately."
		}
		if check.Status != "passed" {
			report.OK = false
		}
		report.Checks = append(report.Checks, check)
	}
	return report
}
func runSmokeCase(ctx context.Context, client *Client, catalog Catalog, fixture SmokeCase) bool {
	var exchange Exchange
	var err error
	switch fixture.Kind {
	case "tool":
		found := false
		for _, tool := range catalog.Tools {
			if tool.Name == fixture.Target {
				found = true
				exchange, err = client.Call(ctx, tool, fixture.Arguments, false)
				break
			}
		}
		if !found {
			return false
		}
	case "resource":
		exchange, err = client.ReadResource(ctx, fixture.Target)
	case "prompt":
		exchange, err = client.GetPrompt(ctx, fixture.Target, fixture.PromptArguments)
	case "auth":
		probe, e := NewClient(client.Endpoint, "", client.Version)
		if e != nil {
			return false
		}
		probe.HTTP = client.HTTP
		if fixture.Credential == "malformed" {
			probe.Token = "gregale-invalid-bearer-probe"
		}
		exchange, err = probe.request(ctx, "initialize", map[string]any{"protocolVersion": client.Version, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "gregale-smoke", "version": "1"}}, nil, false)
		return err != nil && exchange.HTTPStatus == fixture.ExpectedStatus
	}
	if err != nil {
		return false
	}
	if len(fixture.ExpectedResult) > 0 && !smokeJSONEqual(fixture.ExpectedResult, exchange.Result) {
		return false
	}
	if fixture.Kind == "resource" {
		var result struct {
			Contents []struct {
				URI      string  `json:"uri"`
				MIMEType string  `json:"mimeType"`
				Text     *string `json:"text"`
				Blob     *string `json:"blob"`
			} `json:"contents"`
		}
		if json.Unmarshal(exchange.Result, &result) != nil || len(result.Contents) == 0 {
			return false
		}
		for _, content := range result.Contents {
			if content.URI != fixture.Target || content.MIMEType != fixture.MIMEType || (content.Text == nil) == (content.Blob == nil) {
				return false
			}
		}
	}
	if fixture.Kind == "prompt" {
		var result struct {
			Messages []struct {
				Role    string         `json:"role"`
				Content map[string]any `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(exchange.Result, &result) != nil || len(result.Messages) != len(fixture.MessageRoles) {
			return false
		}
		for i, message := range result.Messages {
			if message.Role != fixture.MessageRoles[i] || !validSmokeContent(message.Content) {
				return false
			}
		}
	}
	return true
}
func validSmokeContent(content map[string]any) bool {
	switch content["type"] {
	case "text":
		_, ok := content["text"].(string)
		return ok
	case "image", "audio":
		_, data := content["data"].(string)
		_, mime := content["mimeType"].(string)
		return data && mime
	case "resource":
		_, ok := content["resource"].(map[string]any)
		return ok
	case "resource_link":
		uri, ok := content["uri"].(string)
		return ok && uri != ""
	}
	return false
}
func smokeJSONEqual(expected, actual []byte) bool {
	decode := func(body []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	a, errA := decode(expected)
	b, errB := decode(actual)
	return errA == nil && errB == nil && reflect.DeepEqual(a, b)
}
