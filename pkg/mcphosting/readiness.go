package mcphosting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ReadinessReport contains bounded diagnostics, never server payloads or credentials.
// It is an observation, not a durable authorization to promote a deployment.
type ReadinessReport struct {
	CatalogFormat   int       `json:"catalog_format"`
	OK              bool      `json:"ok"`
	ObservedAt      time.Time `json:"observed_at"`
	ProtocolVersion string    `json:"protocol_version"`
	CatalogSHA256   string    `json:"catalog_sha256,omitempty"`
	Tools           int       `json:"tools"`
	Checks          []Check   `json:"checks"`
}

type readinessTransport struct {
	next    http.RoundTripper
	version string
}

func (t readinessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res, nil
	}
	sessionCreated := len(res.Header.Values("MCP-Session-Id")) != 0
	// A notification's 202 Accepted carries no body and no protocol version
	// to echo; every Gregale starter answers notifications/initialized with
	// a bare 202, so requiring the header failed readiness for all of them
	// (Hunt 14 H14-2). Responses with a body must still name the version.
	versionMismatch := res.StatusCode != http.StatusAccepted &&
		(len(res.Header.Values("MCP-Protocol-Version")) != 1 || res.Header.Get("MCP-Protocol-Version") != t.version)
	if sessionCreated || versionMismatch {
		_ = res.Body.Close()
		return nil, errors.New("invalid stateless protocol response headers")
	}
	return res, nil
}

// CheckReadiness performs only initialize, initialized notification, ping and
// catalog metadata reads. It never calls tools, reads resources or renders prompts.
// baseline optionally pins the exact role-visible contract, including additions.
func CheckReadiness(ctx context.Context, source *Client, baseline *Contract) ReadinessReport {
	return checkReadiness(ctx, source, baseline, 0)
}

// CheckReadinessDigest checks a platform-pinned role catalog without accepting
// caller-submitted success evidence or retaining a full catalog in receipts.
func CheckReadinessDigest(ctx context.Context, source *Client, format int, digest string) ReadinessReport {
	if (format != 1 && format != 2) || len(digest) != 64 {
		return ReadinessReport{ObservedAt: time.Now().UTC(), Checks: []Check{{Name: "configuration", Status: "failed", Detail: "Invalid catalog expectation"}}}
	}
	report := checkReadiness(ctx, source, nil, format)
	if report.OK {
		check := Check{Name: "expected_catalog", Status: "passed"}
		if report.CatalogSHA256 != digest {
			report.OK = false
			check.Status = "failed"
			check.Detail = "Role-visible catalog differs from the expected baseline"
		}
		report.Checks = append(report.Checks, check)
	}
	return report
}
func checkReadiness(ctx context.Context, source *Client, baseline *Contract, format int) ReadinessReport {
	report := ReadinessReport{ObservedAt: time.Now().UTC(), Checks: []Check{}}
	add := func(name, detail string, err error) bool {
		check := Check{Name: name, Status: "passed"}
		if err != nil {
			check.Status = "failed"
			check.Detail = detail
		}
		report.Checks = append(report.Checks, check)
		return err == nil
	}
	if source == nil || source.HTTP == nil {
		add("configuration", "MCP client unavailable", errors.New("missing client"))
		return report
	}
	report.ProtocolVersion = source.Version
	if baseline != nil && (!add("baseline", "Invalid baseline or protocol mismatch", baseline.Validate()) || !add("baseline_protocol", "Baseline protocol differs from requested protocol", readinessRequire(baseline.ProtocolVersion == source.Version))) {
		return report
	}
	// Use a fresh protocol client so diagnostics never share RPC IDs or discovery state.
	client, err := NewClient(source.Endpoint, source.Token, source.Version)
	if !add("configuration", "Invalid MCP endpoint or protocol", err) {
		return report
	}
	hc := *source.HTTP
	next := hc.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	hc.Transport = readinessTransport{next: next, version: client.Version}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.HTTP = &hc
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !add("initialize", "Initialize negotiation, authorization or stateless transport failed", client.InitializeStateless(bounded)) {
		return report
	}
	ping, err := client.request(bounded, "ping", nil, nil, false)
	if err == nil {
		var value map[string]json.RawMessage
		err = json.Unmarshal(ping.Result, &value)
		if err == nil && value == nil {
			err = errors.New("ping result must be an object")
		}
	}
	if !add("ping", "Ping did not return a valid response", err) {
		return report
	}
	var contract Contract
	if (baseline != nil && baseline.Version == 2) || format == 2 || (format == 0 && baseline == nil && client.Capabilities.Tools == nil) {
		catalog, exchange, e := client.DiscoverCatalog(bounded)
		err = e
		if err == nil && len(exchange.RejectedTools) > 0 {
			err = errors.New("incomplete catalog")
		}
		if err == nil {
			contract, err = NewCatalogContract(client.Version, catalog)
		}
	} else {
		tools, exchange, e := client.Tools(bounded)
		err = e
		if err == nil && len(exchange.RejectedTools) > 0 {
			err = errors.New("incomplete tools")
		}
		if err == nil {
			contract, err = NewContract(client.Version, tools)
		}
	}
	if !add("catalog", "Catalog metadata could not be completely validated", err) {
		return report
	}
	report.Tools = len(contract.Tools)
	report.CatalogFormat = contract.Version
	body, err := MarshalContract(contract)
	if !add("catalog_digest", "Catalog could not be canonicalized", err) {
		return report
	}
	digest := sha256.Sum256(body)
	report.CatalogSHA256 = hex.EncodeToString(digest[:])
	if baseline != nil {
		expected, e := MarshalContract(*baseline)
		if e == nil && !bytes.Equal(expected, body) {
			e = errors.New("catalog differs")
		}
		if !add("expected_catalog", "Role-visible catalog differs from the expected baseline", e) {
			return report
		}
	}
	report.OK = true
	return report
}
func readinessRequire(ok bool) error {
	if !ok {
		return errors.New("requirement not met")
	}
	return nil
}
