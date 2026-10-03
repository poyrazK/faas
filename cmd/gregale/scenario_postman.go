package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const postmanDocumentLimit = 5 << 20

var postmanVariablePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
var postmanCamelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var postmanFieldSeparators = regexp.MustCompile(`[^a-z0-9]+`)

type postmanVariable struct {
	Key      string          `json:"key"`
	ID       string          `json:"id"`
	Value    json.RawMessage `json:"value"`
	Disabled bool            `json:"disabled"`
}

type postmanEvent struct {
	Disabled bool `json:"disabled"`
	Script   struct {
		Exec json.RawMessage `json:"exec"`
		Src  json.RawMessage `json:"src"`
	} `json:"script"`
}

type postmanParam struct {
	Key      string  `json:"key"`
	Value    *string `json:"value"`
	Disabled bool    `json:"disabled"`
}

type postmanItem struct {
	Name     string            `json:"name"`
	Items    []postmanItem     `json:"item"`
	Request  json.RawMessage   `json:"request"`
	Auth     json.RawMessage   `json:"auth"`
	Variable []postmanVariable `json:"variable"`
	Events   []postmanEvent    `json:"event"`
	Behavior map[string]any    `json:"protocolProfileBehavior"`
	Response []struct {
		Code *int `json:"code"`
	} `json:"response"`
}

type postmanInput struct {
	Variable string `json:"variable"`
	Field    string `json:"field"`
}

type postmanImportResult struct {
	Manifest       string         `json:"manifest"`
	Requests       int            `json:"requests"`
	RequiredData   []postmanInput `json:"required_data"`
	ScriptsOmitted int            `json:"scripts_omitted"`
	StatusDefaults int            `json:"status_defaults"`
}

type postmanImporter struct {
	requestsOnly   bool
	defaultStatus  int
	explicitStatus bool
	origin         string
	steps          []testHTTPRequest
	variables      map[string]string
	fields         map[string]string
	usedNames      map[string]bool
	scripts        int
	statusDefaults int
}

func cmdTestImport(args []string) int {
	fs := newFlagSet("test import", flag.ContinueOnError)
	from := fs.String("from", "", "local Postman Collection v2.1 JSON file")
	project := fs.String("project", "", "Gregale project slug")
	source := fs.String("source", ".", "command working directory")
	scenario := fs.String("scenario", "api-collection", "scenario name")
	output := fs.String("output", "gregale-test.yaml", "new manifest path; must not exist")
	requestsOnly := fs.Bool("requests-only", false, "explicitly omit Postman scripts and generate draft native checks")
	status := fs.Int("status", 200, "fallback expected status for requests without one saved response status")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *from == "" || *project == "" {
		PrintUsage(osStderr, "usage: gregale test import --from collection.json --project PROJECT [--source DIR] [--scenario NAME] [--output PATH] [--requests-only] [--status CODE]", "test")
		return 1
	}
	if !api.ValidAppSlug(*project) || len(*scenario) < 3 || !testHTTPNamePattern.MatchString(*scenario) || *source == "" || *output == "" || *status < 100 || *status > 599 {
		return printErr("Invalid import options", errors.New("project, scenario, source, output, or status is invalid"))
	}
	file, err := openCustomerFile(*from)
	if err != nil {
		return printErr("Could not read Postman collection", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, postmanDocumentLimit+1))
	if err != nil {
		return printErr("Could not read Postman collection", err)
	}
	if len(body) > postmanDocumentLimit {
		return printErr("Invalid Postman collection", errors.New("collection exceeds 5 MiB"))
	}
	explicitStatus := false
	fs.Visit(func(selected *flag.Flag) { explicitStatus = explicitStatus || selected.Name == "status" })
	importer := postmanImporter{requestsOnly: *requestsOnly, defaultStatus: *status, explicitStatus: explicitStatus,
		variables: map[string]string{}, fields: map[string]string{}, usedNames: map[string]bool{}}
	if err := importer.load(body); err != nil {
		return printErr("Could not import Postman collection", err)
	}
	if err := writeTestHTTPManifest(*output, *project, *source, *scenario, importer.steps); err != nil {
		return printErr("Could not create test manifest", err)
	}
	result := postmanImportResult{Manifest: *output, Requests: len(importer.steps), RequiredData: []postmanInput{}, ScriptsOmitted: importer.scripts, StatusDefaults: importer.statusDefaults}
	for variable, field := range importer.variables {
		result.RequiredData = append(result.RequiredData, postmanInput{Variable: variable, Field: field})
	}
	sort.Slice(result.RequiredData, func(i, j int) bool { return result.RequiredData[i].Field < result.RequiredData[j].Field })
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Created %s with %d native requests (%d fallback status checks). Review expected statuses and add business assertions.\n", *output, result.Requests, result.StatusDefaults)
	if result.ScriptsOmitted > 0 {
		_, _ = fmt.Fprintf(osStdout, "Omitted %d Postman scripts as requested; their assertions, captures, and setup must be added to the native scenario.\n", result.ScriptsOmitted)
	}
	for _, input := range result.RequiredData {
		_, _ = fmt.Fprintf(osStdout, "Case input: %s -> ${data.%s}\n", input.Variable, input.Field)
	}
	return 0
}

func (p *postmanImporter) load(body []byte) error {
	var collection struct {
		Info struct {
			Schema string `json:"schema"`
		} `json:"info"`
		Item     []postmanItem     `json:"item"`
		Auth     json.RawMessage   `json:"auth"`
		Variable []postmanVariable `json:"variable"`
		Event    []postmanEvent    `json:"event"`
		Behavior map[string]any    `json:"protocolProfileBehavior"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&collection); err != nil {
		return errors.New("collection must be valid Postman Collection v2.1 JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("collection must contain exactly one JSON document")
	}
	switch collection.Info.Schema {
	case "https://schema.getpostman.com/json/collection/v2.1.0/collection.json", "https://schema.postman.com/json/collection/v2.1.0/collection.json":
	default:
		return errors.New("only Postman Collection v2.1 exports are supported")
	}
	if len(collection.Behavior) > 0 {
		return errors.New("collection protocolProfileBehavior is unsupported")
	}
	if err := p.events(collection.Event); err != nil {
		return err
	}
	defaults, err := postmanDefaults(nil, collection.Variable)
	if err != nil {
		return err
	}
	if err := p.items(collection.Item, collection.Auth, defaults, 0); err != nil {
		return err
	}
	if len(p.steps) == 0 {
		return errors.New("collection contains no requests")
	}
	fields := make([]string, 0, len(p.fields))
	for field := range p.fields {
		fields = append(fields, field)
	}
	return validateTestHTTPRequestsWithData(testScenario{Requests: p.steps}, fields)
}

func (p *postmanImporter) events(events []postmanEvent) error {
	for _, event := range events {
		if event.Disabled {
			continue
		}
		exec := strings.TrimSpace(string(event.Script.Exec))
		src := strings.TrimSpace(string(event.Script.Src))
		if (exec == "" || exec == "null" || exec == "[]" || exec == `""`) && (src == "" || src == "null" || src == `""`) {
			continue
		}
		if !p.requestsOnly {
			return errors.New("enabled Postman scripts cannot be translated automatically; use --requests-only to explicitly omit them, then add native assertions, captures, and setup")
		}
		p.scripts++
	}
	return nil
}

func postmanDefaults(parent map[string]string, variables []postmanVariable) (map[string]string, error) {
	result := make(map[string]string, len(parent)+len(variables))
	for key, value := range parent {
		result[key] = value
	}
	seen := map[string]bool{}
	for _, variable := range variables {
		if variable.Disabled {
			continue
		}
		key := variable.Key
		if key == "" {
			key = variable.ID
		}
		if key == "" || seen[key] {
			return nil, errors.New("variable list has an empty or duplicate key")
		}
		seen[key] = true
		var value string
		_ = json.Unmarshal(variable.Value, &value) // Only origin strings are inspected; other values remain runtime case inputs.
		result[key] = value
	}
	return result, nil
}

func (p *postmanImporter) items(items []postmanItem, auth json.RawMessage, defaults map[string]string, depth int) error {
	if depth > 16 {
		return errors.New("collection folders exceed 16 levels")
	}
	for _, item := range items {
		if len(item.Behavior) > 0 {
			return errors.New("item protocolProfileBehavior is unsupported")
		}
		if err := p.events(item.Events); err != nil {
			return err
		}
		itemDefaults, err := postmanDefaults(defaults, item.Variable)
		if err != nil {
			return err
		}
		itemAuth := auth
		if len(item.Auth) > 0 && string(item.Auth) != "null" {
			itemAuth = item.Auth
		}
		if len(item.Items) > 0 {
			if len(item.Request) > 0 {
				return errors.New("collection item cannot be both a folder and a request")
			}
			if err := p.items(item.Items, itemAuth, itemDefaults, depth+1); err != nil {
				return err
			}
			continue
		}
		if len(item.Request) == 0 {
			continue // Empty folders have no request to import.
		}
		if len(p.steps) >= 100 {
			return errors.New("collection exceeds 100 requests; split it into smaller scenarios")
		}
		step, err := p.request(item, itemAuth, itemDefaults)
		if err != nil {
			return fmt.Errorf("request %d: %w", len(p.steps)+1, err)
		}
		p.steps = append(p.steps, step)
	}
	return nil
}

func (p *postmanImporter) template(input string, escape func(string) string) (string, error) {
	if strings.Contains(input, "${") || strings.Contains(postmanVariablePattern.ReplaceAllString(input, ""), "{{") {
		return "", errors.New("malformed or incompatible variable reference")
	}
	var problem error
	result := postmanVariablePattern.ReplaceAllStringFunc(input, func(match string) string {
		variable := match[2 : len(match)-2]
		field, err := p.field(variable)
		if err != nil {
			problem = err
			return ""
		}
		return "${data." + field + "}"
	})
	if problem != nil {
		return "", problem
	}
	if escape == nil {
		return result, nil
	}
	// Escape literal query components while leaving native references intact.
	var encoded strings.Builder
	previous := 0
	for _, match := range testSecretReferencePattern.FindAllStringIndex(result, -1) {
		encoded.WriteString(escape(result[previous:match[0]]))
		encoded.WriteString(result[match[0]:match[1]])
		previous = match[1]
	}
	encoded.WriteString(escape(result[previous:]))
	return encoded.String(), nil
}

func (p *postmanImporter) field(variable string) (string, error) {
	if strings.HasPrefix(variable, "$") || strings.HasPrefix(variable, "vault:") {
		return "", errors.New("dynamic and vault variables are unsupported; replace them with named case inputs")
	}
	if field, ok := p.variables[variable]; ok {
		return field, nil
	}
	field := strings.Trim(postmanFieldSeparators.ReplaceAllString(strings.ToLower(postmanCamelBoundary.ReplaceAllString(variable, "${1}_${2}")), "_"), "_")
	if field != "" && field[0] >= '0' && field[0] <= '9' {
		field = "var_" + field
	}
	if !testTriggerKeyPattern.MatchString(field) {
		return "", errors.New("variable name cannot map to a case field; use a name of at most 64 lowercase letters, digits, and underscores")
	}
	if previous, exists := p.fields[field]; exists && previous != variable {
		return "", fmt.Errorf("variables collide on case field %q; rename them before import", field)
	}
	if len(p.fields) >= 32 {
		return "", errors.New("requests use more than 32 case fields")
	}
	p.variables[variable], p.fields[field] = field, variable
	return field, nil
}

func (p *postmanImporter) request(item postmanItem, auth json.RawMessage, defaults map[string]string) (testHTTPRequest, error) {
	var request struct {
		Method      string          `json:"method"`
		URL         json.RawMessage `json:"url"`
		Header      []postmanParam  `json:"header"`
		Auth        json.RawMessage `json:"auth"`
		Body        json.RawMessage `json:"body"`
		Proxy       json.RawMessage `json:"proxy"`
		Certificate json.RawMessage `json:"certificate"`
	}
	var shorthand string
	if err := json.Unmarshal(item.Request, &shorthand); err == nil {
		request.Method = http.MethodGet
		request.URL, _ = json.Marshal(shorthand)
	} else if err := json.Unmarshal(item.Request, &request); err != nil {
		return testHTTPRequest{}, errors.New("request must be an object or URL string with structured headers")
	}
	if request.Method == "" {
		request.Method = http.MethodGet
	}
	if postmanPresent(request.Proxy) || postmanPresent(request.Certificate) {
		return testHTTPRequest{}, errors.New("custom proxies and certificates are unsupported")
	}
	path, origin, err := p.path(request.URL, defaults)
	if err != nil {
		return testHTTPRequest{}, err
	}
	if p.origin != "" && p.origin != origin {
		return testHTTPRequest{}, errors.New("requests must share one origin; split external services into separate scenarios")
	}
	p.origin = origin
	step := testHTTPRequest{Name: p.name(item.Name), Method: request.Method, Path: path, Headers: map[string]string{}}
	for _, header := range request.Header {
		if header.Disabled {
			continue
		}
		key := http.CanonicalHeaderKey(header.Key)
		if _, duplicate := step.Headers[key]; duplicate {
			return testHTTPRequest{}, errors.New("duplicate enabled request headers are unsupported")
		}
		value := ""
		if header.Value != nil {
			value = *header.Value
		}
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "X-Api-Key") {
			credential := value
			if strings.EqualFold(key, "Authorization") {
				for _, scheme := range []string{"Bearer ", "Basic "} {
					if token, ok := strings.CutPrefix(value, scheme); ok {
						credential = token
						break
					}
				}
			}
			if credential == "" || postmanVariablePattern.FindString(credential) != credential {
				return testHTTPRequest{}, errors.New("authentication headers must use a complete named variable; literal credentials are not copied")
			}
		}
		expanded, err := p.template(value, nil)
		if err != nil {
			return testHTTPRequest{}, err
		}
		step.Headers[key] = expanded
	}
	if postmanPresent(request.Auth) {
		auth = request.Auth
	}
	if err := p.auth(auth, step.Headers); err != nil {
		return testHTTPRequest{}, err
	}
	step.JSON, err = p.body(request.Body)
	if err != nil {
		return testHTTPRequest{}, err
	}
	statuses := map[int]bool{}
	for _, response := range item.Response {
		if response.Code == nil {
			continue
		}
		if *response.Code < 100 || *response.Code > 599 {
			return testHTTPRequest{}, errors.New("saved response status is invalid")
		}
		statuses[*response.Code] = true
	}
	if len(statuses) > 1 && !p.explicitStatus {
		return testHTTPRequest{}, errors.New("saved responses have different statuses; pass --status CODE to choose the draft expectation explicitly")
	}
	step.Expect.Status = p.defaultStatus
	if len(statuses) == 1 {
		for status := range statuses {
			step.Expect.Status = status
		}
	} else {
		p.statusDefaults++
	}
	return step, nil
}

func postmanPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func (p *postmanImporter) name(label string) string {
	base := "request-" + sanitizeSlug(label)
	if label == "" {
		base = "request"
	}
	if len(base) > 56 {
		base = base[:56]
	}
	name := base
	for suffix := 2; p.usedNames[name]; suffix++ {
		name = base + "-" + strconv.Itoa(suffix)
	}
	p.usedNames[name] = true
	return name
}

func (p *postmanImporter) auth(raw json.RawMessage, headers map[string]string) error {
	if !postmanPresent(raw) {
		return nil
	}
	var auth struct {
		Type   string         `json:"type"`
		Bearer []postmanParam `json:"bearer"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return errors.New("malformed authentication declaration")
	}
	if auth.Type == "noauth" {
		return nil
	}
	if auth.Type != "bearer" {
		return errors.New("only noauth and bearer authentication are supported")
	}
	if _, exists := headers["Authorization"]; exists {
		return errors.New("bearer authentication conflicts with an Authorization header")
	}
	if len(auth.Bearer) != 1 || auth.Bearer[0].Disabled || auth.Bearer[0].Key != "token" || auth.Bearer[0].Value == nil || *auth.Bearer[0].Value == "" || postmanVariablePattern.FindString(*auth.Bearer[0].Value) != *auth.Bearer[0].Value {
		return errors.New("bearer authentication must use a named token variable; literal credentials are not copied")
	}
	value, err := p.template(*auth.Bearer[0].Value, nil)
	if err != nil {
		return err
	}
	headers["Authorization"] = "Bearer " + value
	return nil
}

func (p *postmanImporter) body(raw json.RawMessage) (any, error) {
	if !postmanPresent(raw) {
		return nil, nil
	}
	var body struct {
		Mode     string `json:"mode"`
		Raw      string `json:"raw"`
		Disabled bool   `json:"disabled"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("malformed request body")
	}
	if body.Disabled {
		return nil, nil
	}
	if body.Mode != "raw" {
		return nil, errors.New("only raw JSON bodies are supported; form, file, GraphQL, and binary bodies require native commands")
	}
	// JSON templates may use an unquoted variable for a number or boolean.
	// Quote that placeholder in YAML; the case runner restores the input type.
	var converted strings.Builder
	inString, escaped, previous := false, false, 0
	for _, match := range postmanVariablePattern.FindAllStringIndex(body.Raw, -1) {
		plain := body.Raw[previous:match[0]]
		for _, char := range plain {
			if escaped {
				escaped = false
			} else if char == '\\' && inString {
				escaped = true
			} else if char == '"' {
				inString = !inString
			}
		}
		converted.WriteString(plain)
		ref, err := p.template(body.Raw[match[0]:match[1]], nil)
		if err != nil {
			return nil, err
		}
		if inString {
			converted.WriteString(strings.TrimSuffix(ref, "}") + ".string}")
		} else {
			quoted, _ := json.Marshal(ref)
			converted.Write(quoted)
		}
		previous = match[1]
	}
	converted.WriteString(body.Raw[previous:])
	if strings.Contains(postmanVariablePattern.ReplaceAllString(body.Raw, ""), "{{") || strings.Contains(body.Raw, "${") {
		return nil, errors.New("malformed or incompatible body variable reference")
	}
	decoder := json.NewDecoder(strings.NewReader(converted.String()))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("raw body must be a JSON value other than null")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("raw body must contain exactly one JSON value")
	}
	return postmanJSONNumbers(value)
}

func (p *postmanImporter) path(raw json.RawMessage, defaults map[string]string) (string, string, error) {
	var text string
	var spec struct {
		Raw      string            `json:"raw"`
		Protocol string            `json:"protocol"`
		Host     json.RawMessage   `json:"host"`
		Path     json.RawMessage   `json:"path"`
		Port     string            `json:"port"`
		Hash     string            `json:"hash"`
		Query    *[]postmanParam   `json:"query"`
		Variable []postmanVariable `json:"variable"`
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		if err := json.Unmarshal(raw, &spec); err != nil {
			return "", "", errors.New("URL must be a string or structured URL object")
		}
		if spec.Hash != "" {
			return "", "", errors.New("URL fragments are unsupported")
		}
		text = spec.Raw
		if text == "" {
			host, err := postmanURLPart(spec.Host, ".")
			if err != nil {
				return "", "", err
			}
			path, err := postmanURLPart(spec.Path, "/")
			if err != nil {
				return "", "", err
			}
			text = host
			if spec.Port != "" {
				text += ":" + spec.Port
			}
			if spec.Protocol != "" {
				text = spec.Protocol + "://" + text
			}
			text += "/" + strings.TrimPrefix(path, "/")
		}
	}
	path, origin, err := postmanRelativePath(text, defaults)
	if err != nil {
		return "", "", err
	}
	pathPart, queryPart, hasQuery := strings.Cut(path, "?")
	pathVars := map[string]bool{}
	for _, variable := range spec.Variable {
		key := variable.Key
		if key == "" {
			key = variable.ID
		}
		if !variable.Disabled {
			pathVars[key] = true
		}
	}
	parts := strings.Split(pathPart, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			key := strings.TrimPrefix(part, ":")
			if !pathVars[key] {
				return "", "", errors.New("URL path parameter has no enabled variable declaration")
			}
			parts[i] = "{{" + key + "}}"
		}
	}
	pathPart, err = p.template(strings.Join(parts, "/"), nil)
	if err != nil {
		return "", "", err
	}
	if spec.Query != nil {
		query := make([]string, 0, len(*spec.Query))
		for _, param := range *spec.Query {
			if param.Disabled {
				continue
			}
			key, err := p.template(param.Key, url.QueryEscape)
			if err != nil {
				return "", "", err
			}
			if param.Value != nil {
				value, err := p.template(*param.Value, url.QueryEscape)
				if err != nil {
					return "", "", err
				}
				key += "=" + value
			}
			query = append(query, key)
		}
		queryPart, hasQuery = strings.Join(query, "&"), len(query) > 0
	} else if hasQuery {
		queryPart, err = p.template(queryPart, nil)
		if err != nil {
			return "", "", err
		}
	}
	if hasQuery {
		pathPart += "?" + queryPart
	}
	return pathPart, origin, nil
}

func postmanURLPart(raw json.RawMessage, separator string) (string, error) {
	if !postmanPresent(raw) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("structured URL host and path must be strings or string arrays")
	}
	return strings.Join(parts, separator), nil
}

func postmanRelativePath(raw string, defaults map[string]string) (string, string, error) {
	if strings.Contains(raw, "#") || strings.Contains(raw, "${") {
		return "", "", errors.New("URL fragments and native template syntax are unsupported in an imported URL")
	}
	if strings.HasPrefix(raw, "{{") {
		match := postmanVariablePattern.FindString(raw)
		if match == "" || !strings.HasPrefix(raw, match) {
			return "", "", errors.New("malformed URL origin variable")
		}
		variable := match[2 : len(match)-2]
		if strings.HasPrefix(variable, "$") || strings.HasPrefix(variable, "vault:") {
			return "", "", errors.New("URL origin must use a named variable")
		}
		suffix := strings.TrimPrefix(raw, match)
		if suffix != "" && !strings.HasPrefix(suffix, "/") && !strings.HasPrefix(suffix, "?") {
			return "", "", errors.New("URL origin variable must be followed by a path or query")
		}
		prefix, origin := "", "variable:"+variable
		if value := defaults[variable]; value != "" {
			parsed, err := url.Parse(value)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "{{") || strings.Contains(value, "${") || strings.Contains(value, "#") {
				return "", "", errors.New("origin variable value must be an HTTP URL without credentials, query, fragment, or nested variables")
			}
			prefix, origin = strings.TrimSuffix(parsed.EscapedPath(), "/"), parsed.Scheme+"://"+parsed.Host
		}
		if suffix == "" || strings.HasPrefix(suffix, "?") {
			suffix = "/" + suffix
		}
		return prefix + suffix, origin, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || strings.Contains(parsed.Host, "{{") {
		return "", "", errors.New("URL must be absolute HTTP(S) or begin with a named origin variable")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	path := strings.TrimPrefix(raw, origin)
	if path == "" || strings.HasPrefix(path, "?") {
		path = "/" + path
	}
	return path, origin, nil
}

// Preserve numeric values across JSON -> YAML -> native JSON. Values whose
// decimal representation cannot survive that round trip require case data.
func postmanJSONNumbers(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if len(value) > 1024 {
			return nil, errors.New("JSON number is too large for a native manifest; use a named case input")
		}
		if integer, err := value.Int64(); err == nil {
			return integer, nil
		}
		if integer, err := strconv.ParseUint(string(value), 10, 64); err == nil {
			return integer, nil
		}
		number, err := value.Float64()
		if err == nil {
			if _, exponent, hasExponent := strings.Cut(strings.ToLower(string(value)), "e"); hasExponent {
				exponentValue, parseErr := strconv.Atoi(exponent)
				if parseErr != nil || exponentValue < -400 || exponentValue > 400 {
					return nil, errors.New("JSON number exponent cannot be preserved; use a named case input")
				}
			}
			encoded, marshalErr := json.Marshal(number)
			original, ok := new(big.Rat).SetString(string(value))
			roundTrip, roundTripOK := new(big.Rat).SetString(string(encoded))
			if marshalErr == nil && ok && roundTripOK && original.Cmp(roundTrip) == 0 {
				return number, nil
			}
		}
		return nil, errors.New("JSON number cannot be preserved in a native manifest; use a named case input")
	case map[string]any:
		for key, item := range value {
			if strings.Contains(key, "${") {
				return nil, errors.New("variables in JSON object keys are unsupported")
			}
			converted, err := postmanJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			value[key] = converted
		}
	case []any:
		for i, item := range value {
			converted, err := postmanJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			value[i] = converted
		}
	}
	return value, nil
}
