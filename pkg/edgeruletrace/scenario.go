package edgeruletrace

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	// ScenarioConfigVersion is the current version of the portable trace input format.
	ScenarioConfigVersion = 1
	// MaxScenarioConfigBytes allows for a base64-encoded maximum-size request body
	// plus the surrounding JSON request context.
	MaxScenarioConfigBytes = 2 << 20
)

// ScenarioConfig is the versioned, portable input accepted by the trace CLI.
// Body is UTF-8 text; BodyBase64 is available for arbitrary bytes. At most one
// body field may be present. Header entries use Name:Value strings so repeated
// header names retain their order and values.
type ScenarioConfig struct {
	Version int                   `json:"version"`
	App     string                `json:"app"`
	Request ScenarioRequestConfig `json:"request"`
}

// ScenarioRequestConfig describes only request context used by the simulator;
// it does not contain or fetch app rules.
type ScenarioRequestConfig struct {
	URL        string   `json:"url"`
	Method     string   `json:"method,omitempty"`
	Headers    []string `json:"headers,omitempty"`
	ClientIP   string   `json:"client_ip,omitempty"`
	Country    string   `json:"country,omitempty"`
	Body       *string  `json:"body,omitempty"`
	BodyBase64 *string  `json:"body_base64,omitempty"`
}

// ParseScenarioConfig decodes and validates one portable trace scenario.
// Unknown fields and trailing JSON values are rejected so a typo cannot
// silently change the request being simulated.
func ParseScenarioConfig(data []byte) (Input, error) {
	if len(data) > MaxScenarioConfigBytes {
		return Input{}, fmt.Errorf("trace scenario config must not exceed %d bytes", MaxScenarioConfigBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config ScenarioConfig
	if err := decoder.Decode(&config); err != nil {
		return Input{}, fmt.Errorf("invalid trace scenario config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Input{}, fmt.Errorf("trace scenario config must contain one JSON object")
		}
		return Input{}, fmt.Errorf("invalid trailing data in trace scenario config: %w", err)
	}
	if config.Version != ScenarioConfigVersion {
		return Input{}, fmt.Errorf("unsupported trace scenario config version %d (supported: %d)", config.Version, ScenarioConfigVersion)
	}
	if config.Request.Body != nil && config.Request.BodyBase64 != nil {
		return Input{}, fmt.Errorf("trace scenario request must set only one of body or body_base64")
	}
	u, err := url.Parse(config.Request.URL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return Input{}, fmt.Errorf("request.url must be an absolute HTTP(S) URL without credentials or fragment")
	}
	requestPath := u.Path
	if requestPath == "" {
		requestPath = "/"
	}
	headers, err := ParseRequestHeaders(config.Request.Headers)
	if err != nil {
		return Input{}, fmt.Errorf("request.headers must contain valid Name:Value pairs")
	}
	var body []byte
	bodyProvided := false
	if config.Request.Body != nil {
		body = []byte(*config.Request.Body)
		bodyProvided = true
	} else if config.Request.BodyBase64 != nil {
		decoded, decodeErr := base64.StdEncoding.DecodeString(*config.Request.BodyBase64)
		if decodeErr != nil {
			return Input{}, fmt.Errorf("request.body_base64 must be valid standard base64")
		}
		body = decoded
		bodyProvided = true
	}
	if len(body) > MaxTraceBodyBytes {
		return Input{}, fmt.Errorf("request body must not exceed %d bytes", MaxTraceBodyBytes)
	}
	return NormalizeInput(Input{
		App: config.App, Host: u.Hostname(), Path: requestPath, Method: strings.TrimSpace(config.Request.Method),
		ClientIP: config.Request.ClientIP, Country: config.Request.Country, Headers: headers,
		Body: body, BodyProvided: bodyProvided,
	})
}
