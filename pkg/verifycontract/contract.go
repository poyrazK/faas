// Package verifycontract defines gregale.verify.yaml, the declarative checks
// Gregale runs against a deployment candidate before it takes traffic
// (ADR-20261122220000). The package is shared by the CLI (parse and validate
// before upload), apid (validate on store) and imaged (evaluate responses), so
// a contract that one accepts the others accept too.
package verifycontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// FileName is the contract's conventional name at the root of a project.
const FileName = "gregale.verify.yaml"

// Limits live in pkg/api/limits.go (ADR-20261122220000).
const (
	Version               = 1
	MaxContractBytes      = api.VerificationContractMaxBytes
	MaxChecks             = api.VerificationContractMaxChecks
	DefaultTimeoutSeconds = api.VerificationContractDefaultTimeoutSecs
	MaxTimeoutSeconds     = api.VerificationContractMaxTimeoutSecs
	MaxPathBytes          = api.VerificationContractMaxPathBytes
	MaxHeaders            = api.VerificationContractMaxHeaders
	MaxHeaderValueBytes   = api.VerificationContractMaxHeaderValueBytes
	MaxBodyBytes          = api.VerificationContractMaxBodyBytes
	MaxStatuses           = api.VerificationContractMaxStatuses
	MaxBodyContains       = api.VerificationContractMaxBodyContains
	MaxJSONAssertions     = api.VerificationContractMaxJSONAssertions
	MaxSQLBytes           = api.VerificationContractMaxSQLBytes
	MaxResponseBytes      = api.VerificationContractMaxResponseBytes
	MaxSecretRefs         = api.VerificationContractMaxSecretRefs
	DefaultMCPPath        = "/mcp"
)

// Check kinds.
const (
	KindHTTP = "http"
	KindMCP  = "mcp"
	KindSQL  = "sql"
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid verification contract")

// Contract is one gregale.verify.yaml document.
type Contract struct {
	Version        int     `yaml:"version" json:"version"`
	TimeoutSeconds int     `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	Checks         []Check `yaml:"checks" json:"checks"`
}

// Check is one named check. Exactly one of HTTP, MCP or SQL is set.
type Check struct {
	Name string `yaml:"name" json:"name"`
	// Advisory checks are reported but never block promotion.
	Advisory bool       `yaml:"advisory,omitempty" json:"advisory,omitempty"`
	HTTP     *HTTPCheck `yaml:"http,omitempty" json:"http,omitempty"`
	MCP      *MCPCheck  `yaml:"mcp,omitempty" json:"mcp,omitempty"`
	SQL      *SQLCheck  `yaml:"sql,omitempty" json:"sql,omitempty"`
}

// HTTPCheck sends one request to the candidate.
type HTTPCheck struct {
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	Path    string            `yaml:"path" json:"path"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Body is sent verbatim; JSON is encoded and sent with
	// Content-Type: application/json unless a header sets one.
	Body   string     `yaml:"body,omitempty" json:"body,omitempty"`
	JSON   any        `yaml:"json,omitempty" json:"json,omitempty"`
	Expect HTTPExpect `yaml:"expect,omitempty" json:"expect,omitempty"`
}

// HTTPExpect lists the assertions on one response. With no Status the
// response must be 2xx.
type HTTPExpect struct {
	Status       StatusSet `yaml:"status,omitempty" json:"status,omitempty"`
	MaxLatencyMS int       `yaml:"max_latency_ms,omitempty" json:"max_latency_ms,omitempty"`
	// Headers maps a response header name to a substring its value must hold.
	Headers      map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	BodyContains []string          `yaml:"body_contains,omitempty" json:"body_contains,omitempty"`
	// JSON maps an RFC 6901 pointer ("" is the whole document) to the JSON
	// value the response must hold there.
	JSON map[string]any `yaml:"json,omitempty" json:"json,omitempty"`
}

// MCPCheck runs one MCP operation against the candidate's MCP endpoint.
// Exactly one of Tool, Resource or Prompt is set.
type MCPCheck struct {
	Path            string            `yaml:"path,omitempty" json:"path,omitempty"`
	Tool            string            `yaml:"tool,omitempty" json:"tool,omitempty"`
	Resource        string            `yaml:"resource,omitempty" json:"resource,omitempty"`
	Prompt          string            `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Arguments       map[string]any    `yaml:"arguments,omitempty" json:"arguments,omitempty"`
	PromptArguments map[string]string `yaml:"prompt_arguments,omitempty" json:"prompt_arguments,omitempty"`
	// ExpectedResult must equal the tool result (tools only).
	ExpectedResult any      `yaml:"expected_result,omitempty" json:"expected_result,omitempty"`
	MIMEType       string   `yaml:"mime_type,omitempty" json:"mime_type,omitempty"`
	MessageRoles   []string `yaml:"message_roles,omitempty" json:"message_roles,omitempty"`
}

// SQLCheck runs one read-only statement against a managed Postgres database
// of the app's account, in a READ ONLY transaction as the agent query login.
type SQLCheck struct {
	Database string    `yaml:"database" json:"database"`
	Query    string    `yaml:"query" json:"query"`
	Expect   SQLExpect `yaml:"expect,omitempty" json:"expect,omitempty"`
}

// SQLExpect asserts on the result. Value is the first column of the first row
// in PostgreSQL text form; Null asserts that value is NULL.
type SQLExpect struct {
	Rows  *int    `yaml:"rows,omitempty" json:"rows,omitempty"`
	Value *string `yaml:"value,omitempty" json:"value,omitempty"`
	Null  bool    `yaml:"null,omitempty" json:"null,omitempty"`
}

// StatusSet accepts a single status code or a list of them.
type StatusSet []int

func (s *StatusSet) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var one int
		if err := node.Decode(&one); err != nil {
			return err
		}
		*s = StatusSet{one}
		return nil
	}
	var many []int
	if err := node.Decode(&many); err != nil {
		return err
	}
	*s = many
	return nil
}

func (s *StatusSet) UnmarshalJSON(data []byte) error {
	var one int
	if err := json.Unmarshal(data, &one); err == nil {
		*s = StatusSet{one}
		return nil
	}
	var many []int
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

// Kind names the check's kind.
func (c Check) Kind() string {
	switch {
	case c.HTTP != nil:
		return KindHTTP
	case c.MCP != nil:
		return KindMCP
	case c.SQL != nil:
		return KindSQL
	}
	return ""
}

// Mutating reports whether the check may change state and therefore runs
// only outside production. MCP tool calls are decided at run time from the
// tool's readOnlyHint, so they are not mutating here.
func (c Check) Mutating() bool {
	return c.HTTP != nil && !safeMethod(c.HTTP.EffectiveMethod())
}

// EffectiveMethod is the request method, GET by default.
func (h HTTPCheck) EffectiveMethod() string {
	if h.Method == "" {
		return http.MethodGet
	}
	return strings.ToUpper(h.Method)
}

// EffectivePath is the MCP endpoint path, /mcp by default.
func (m MCPCheck) EffectivePath() string {
	if m.Path == "" {
		return DefaultMCPPath
	}
	return m.Path
}

// Timeout is the whole contract's budget in seconds.
func (c Contract) Timeout() int {
	if c.TimeoutSeconds == 0 {
		return DefaultTimeoutSeconds
	}
	return c.TimeoutSeconds
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// ParseYAML decodes and validates one gregale.verify.yaml document.
func ParseYAML(data []byte) (Contract, error) {
	if len(data) > MaxContractBytes {
		return Contract{}, invalidf("contract must be at most %d bytes", MaxContractBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var c Contract
	if err := decoder.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return Contract{}, invalidf("contract is empty")
		}
		return Contract{}, invalidf("decode YAML: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Contract{}, invalidf("contract must contain one YAML document")
	}
	c = normalizeYAMLValues(c)
	return c, c.Validate()
}

// ParseJSON decodes and validates the contract's JSON form, which the API
// stores and returns.
func ParseJSON(data []byte) (Contract, error) {
	if len(data) > MaxContractBytes {
		return Contract{}, invalidf("contract must be at most %d bytes", MaxContractBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var c Contract
	if err := decoder.Decode(&c); err != nil {
		return Contract{}, invalidf("decode JSON: %v", err)
	}
	if decoder.More() {
		return Contract{}, invalidf("contract must contain one JSON object")
	}
	return c, c.Validate()
}

// Canonical returns the contract's stored JSON form and its SHA-256.
func (c Contract) Canonical() ([]byte, string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, "", err
	}
	// JSON escaping can make the stored form larger than the file that
	// passed the size check; the stored form is what the limit bounds.
	if len(raw) > MaxContractBytes {
		return nil, "", invalidf("contract encodes to %d bytes, more than %d", len(raw), MaxContractBytes)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// forbiddenHeaders are set by the platform or the transport; a contract
// cannot set them.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Transfer-Encoding": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Keep-Alive": true, "Proxy-Connection": true,
}

// Validate checks the whole contract.
func (c Contract) Validate() error {
	if c.Version != Version {
		return invalidf("version must be %d", Version)
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > MaxTimeoutSeconds {
		return invalidf("timeout_seconds must be from 1 to %d", MaxTimeoutSeconds)
	}
	if len(c.Checks) == 0 || len(c.Checks) > MaxChecks {
		return invalidf("checks must hold 1 to %d checks", MaxChecks)
	}
	seen := map[string]bool{}
	for i, check := range c.Checks {
		if !namePattern.MatchString(check.Name) {
			return invalidf("checks[%d]: name must be 1 to 64 lowercase letters, digits, '.', '_' or '-'", i)
		}
		if seen[check.Name] {
			return invalidf("check %q: names must be unique", check.Name)
		}
		seen[check.Name] = true
		kinds := 0
		for _, set := range []bool{check.HTTP != nil, check.MCP != nil, check.SQL != nil} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			return invalidf("check %q: set exactly one of http, mcp or sql", check.Name)
		}
		var err error
		switch {
		case check.HTTP != nil:
			err = check.HTTP.validate()
		case check.MCP != nil:
			err = check.MCP.validate()
		case check.SQL != nil:
			err = check.SQL.validate()
		}
		if err != nil {
			return invalidf("check %q: %v", check.Name, err)
		}
		if misplacedSecretRef(check) {
			return invalidf("check %q: ${secret:NAME} is substituted only in http header values", check.Name)
		}
	}
	return c.validateSecretRefs()
}

func validatePath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > MaxPathBytes ||
		strings.ContainsAny(path, " \r\n\t#") || strings.Contains(path, "://") {
		return fmt.Errorf("path must be an absolute path of at most %d bytes without spaces or a fragment", MaxPathBytes)
	}
	return nil
}

func (h HTTPCheck) validate() error {
	switch h.EffectiveMethod() {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return errors.New("method must be GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE")
	}
	if err := validatePath(h.Path); err != nil {
		return err
	}
	if len(h.Headers) > MaxHeaders {
		return fmt.Errorf("at most %d headers", MaxHeaders)
	}
	for name, value := range h.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if name == "" || strings.ContainsAny(name, " \r\n\t:") || forbiddenHeaders[canonical] ||
			strings.HasPrefix(canonical, "X-Faas-") || strings.HasPrefix(canonical, "X-Gregale-") {
			return fmt.Errorf("header %q cannot be set by a contract", name)
		}
		if len(value) > MaxHeaderValueBytes || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q value must be one line of at most %d bytes", name, MaxHeaderValueBytes)
		}
		if _, err := headerSecretRefs(value); err != nil {
			return fmt.Errorf("header %q: %w", name, err)
		}
	}
	if h.Body != "" && h.JSON != nil {
		return errors.New("set body or json, not both")
	}
	if len(h.Body) > MaxBodyBytes {
		return fmt.Errorf("body must be at most %d bytes", MaxBodyBytes)
	}
	if h.JSON != nil {
		raw, err := json.Marshal(h.JSON)
		if err != nil || len(raw) > MaxBodyBytes {
			return fmt.Errorf("json body must encode to at most %d bytes", MaxBodyBytes)
		}
	}
	if (h.Body != "" || h.JSON != nil) && safeMethod(h.EffectiveMethod()) {
		return errors.New("GET, HEAD and OPTIONS checks cannot send a body")
	}
	return h.Expect.validate()
}

func (e HTTPExpect) validate() error {
	if len(e.Status) > MaxStatuses {
		return fmt.Errorf("expect.status lists at most %d codes", MaxStatuses)
	}
	for _, status := range e.Status {
		if status < 100 || status > 599 {
			return errors.New("expect.status codes must be from 100 to 599")
		}
	}
	if e.MaxLatencyMS < 0 || e.MaxLatencyMS > MaxTimeoutSeconds*1000 {
		return fmt.Errorf("expect.max_latency_ms must be from 1 to %d", MaxTimeoutSeconds*1000)
	}
	if len(e.Headers) > MaxHeaders {
		return fmt.Errorf("expect.headers lists at most %d headers", MaxHeaders)
	}
	for name, value := range e.Headers {
		if name == "" || strings.ContainsAny(name, " \r\n\t:") || len(value) > MaxHeaderValueBytes {
			return fmt.Errorf("expect.headers entry %q is invalid", name)
		}
	}
	if len(e.BodyContains) > MaxBodyContains {
		return fmt.Errorf("expect.body_contains lists at most %d strings", MaxBodyContains)
	}
	for _, s := range e.BodyContains {
		if s == "" || len(s) > MaxHeaderValueBytes {
			return fmt.Errorf("expect.body_contains strings must be 1 to %d bytes", MaxHeaderValueBytes)
		}
	}
	if len(e.JSON) > MaxJSONAssertions {
		return fmt.Errorf("expect.json lists at most %d pointers", MaxJSONAssertions)
	}
	for pointer := range e.JSON {
		if _, err := parsePointer(pointer); err != nil {
			return fmt.Errorf("expect.json key %q: %w", pointer, err)
		}
	}
	return nil
}

func (m MCPCheck) validate() error {
	if err := validatePath(m.EffectivePath()); err != nil {
		return err
	}
	targets := 0
	for _, t := range []string{m.Tool, m.Resource, m.Prompt} {
		if t != "" {
			targets++
			if len(t) > 512 || strings.ContainsAny(t, "\r\n\t") {
				return errors.New("tool, resource and prompt names must be one line of at most 512 bytes")
			}
		}
	}
	if targets != 1 {
		return errors.New("set exactly one of tool, resource or prompt")
	}
	switch {
	case m.Tool != "":
		if m.ExpectedResult == nil || m.PromptArguments != nil || m.MIMEType != "" || m.MessageRoles != nil {
			return errors.New("tool checks take arguments and require expected_result")
		}
	case m.Resource != "":
		if m.MIMEType == "" || m.Arguments != nil || m.PromptArguments != nil || m.MessageRoles != nil || m.ExpectedResult != nil {
			return errors.New("resource checks require mime_type and take no arguments")
		}
	case m.Prompt != "":
		if len(m.MessageRoles) == 0 || m.Arguments != nil || m.MIMEType != "" || m.ExpectedResult != nil {
			return errors.New("prompt checks require message_roles and take prompt_arguments")
		}
		for _, role := range m.MessageRoles {
			if role != "user" && role != "assistant" {
				return errors.New("message_roles must be user or assistant")
			}
		}
	}
	if m.Arguments != nil {
		if _, err := json.Marshal(m.Arguments); err != nil {
			return errors.New("arguments must be JSON values")
		}
	}
	if m.ExpectedResult != nil {
		if _, err := json.Marshal(m.ExpectedResult); err != nil {
			return errors.New("expected_result must be a JSON value")
		}
	}
	return nil
}

var databaseNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func (s SQLCheck) validate() error {
	if !databaseNamePattern.MatchString(s.Database) {
		return errors.New("database must name a managed Postgres database of the account")
	}
	if strings.TrimSpace(s.Query) == "" || len(s.Query) > MaxSQLBytes || strings.ContainsRune(s.Query, 0) {
		return fmt.Errorf("query must be 1 to %d bytes without NUL", MaxSQLBytes)
	}
	if s.Expect.Rows != nil && (*s.Expect.Rows < 0 || *s.Expect.Rows > 1000) {
		return errors.New("expect.rows must be from 0 to 1000")
	}
	if s.Expect.Null && s.Expect.Value != nil {
		return errors.New("set expect.value or expect.null, not both")
	}
	if s.Expect.Rows == nil && s.Expect.Value == nil && !s.Expect.Null {
		return errors.New("expect must assert rows, value or null")
	}
	return nil
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// normalizeYAMLValues turns YAML-decoded free-form values (maps with any
// keys) into JSON-compatible values so the JSON form round-trips.
func normalizeYAMLValues(c Contract) Contract {
	for i := range c.Checks {
		if h := c.Checks[i].HTTP; h != nil {
			h.JSON = jsonCompatible(h.JSON)
			for k, v := range h.Expect.JSON {
				h.Expect.JSON[k] = jsonCompatible(v)
			}
		}
		if m := c.Checks[i].MCP; m != nil {
			for k, v := range m.Arguments {
				m.Arguments[k] = jsonCompatible(v)
			}
			m.ExpectedResult = jsonCompatible(m.ExpectedResult)
		}
	}
	return c
}

func jsonCompatible(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			t[k] = jsonCompatible(inner)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			out[fmt.Sprint(k)] = jsonCompatible(inner)
		}
		return out
	case []any:
		for i := range t {
			t[i] = jsonCompatible(t[i])
		}
		return t
	default:
		return v
	}
}
