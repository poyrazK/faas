// adr: 20261017201006
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

// These are versioned protocol bounds, not account quotas.
const (
	ScenarioSimulationVersion         = 1
	ScenarioSimulationSnapshotVersion = 2
	ScenarioSimulationScope           = "managed_webhooks_and_simulated_outbound"
	MaxScenarioSimulationFixtures     = 64
	MaxScenarioSimulationPlanBytes    = 64 << 10
	MaxScenarioSimulationRequestBytes = 64 << 10
	MaxScenarioSimulationAttempts     = 256
)

var simulationName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var simulationIntegration = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var simulationField = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

type ScenarioSimulationPlan struct {
	Version  int                         `json:"version" yaml:"version"`
	Fixtures []ScenarioSimulationFixture `json:"fixtures" yaml:"fixtures"`
}

type ScenarioSimulationFixture struct {
	Workload      string                       `json:"workload" yaml:"workload"`
	Name          string                       `json:"name" yaml:"name"`
	Integration   string                       `json:"integration" yaml:"integration"`
	Adapter       string                       `json:"adapter" yaml:"adapter"`
	Method        string                       `json:"method" yaml:"method"`
	Path          string                       `json:"path" yaml:"path"`
	CaptureFields []string                     `json:"capture_fields,omitempty" yaml:"capture_fields,omitempty"`
	Responses     []ScenarioSimulationResponse `json:"responses" yaml:"responses"`
}

type ScenarioSimulationResponse struct {
	Status            int    `json:"status,omitempty" yaml:"status,omitempty"`
	Body              string `json:"body,omitempty" yaml:"body,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty" yaml:"retry_after_seconds,omitempty"`
	Fault             string `json:"fault,omitempty" yaml:"fault,omitempty"`
	DelayMS           int    `json:"delay_ms,omitempty" yaml:"delay_ms,omitempty"`
}

// Only allowlisted top-level JSON fields are retained. Headers and query values
// are never exported. Hashes retain equality without storing credentials.
type ScenarioSimulationAttempt struct {
	ID                string          `json:"id"`
	Workload          string          `json:"workload"`
	Fixture           string          `json:"fixture"`
	Integration       string          `json:"integration"`
	Adapter           string          `json:"adapter"`
	Method            string          `json:"method"`
	Path              string          `json:"path"`
	RequestSHA256     string          `json:"request_sha256"`
	IdempotencySHA256 string          `json:"idempotency_sha256,omitempty"`
	Payload           json.RawMessage `json:"payload"`
	Attempt           int             `json:"attempt"`
	Status            string          `json:"status"`
	ResponseStatus    int             `json:"response_status"`
	Fault             string          `json:"fault,omitempty"`
}

// ScenarioSimulationRequest is an internal authenticated command. Its identity
// comes from the host relay, never from request headers or the request body.
type ScenarioSimulationRequest struct {
	RecorderRequestID                         string
	AccountID, AppID, RunID, InstanceID       string
	Integration, Method, Path, IdempotencyKey string
	Body                                      []byte
	Invalid                                   bool
}

type ScenarioSimulationDecision struct {
	Attempt         ScenarioSimulationAttempt
	Response        ScenarioSimulationResponse
	WorkerFaultID   string
	SuspendResumeID string
}

func ScenarioSimulationDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// NormalizeScenarioSimulationPlan validates an immutable fixture manifest and
// gives both revisions the same identity independently of declaration order.
func NormalizeScenarioSimulationPlan(plan ScenarioSimulationPlan, workloads []string) ([]byte, string, error) {
	if plan.Version != ScenarioSimulationVersion || len(plan.Fixtures) == 0 || len(plan.Fixtures) > MaxScenarioSimulationFixtures {
		return nil, "", errors.New("simulation requires version 1 and 1 to 64 fixtures")
	}
	known := make(map[string]bool, len(workloads))
	for _, name := range workloads {
		known[name] = true
	}
	seen, routes := map[string]bool{}, map[string]bool{}
	plan.Fixtures = append([]ScenarioSimulationFixture(nil), plan.Fixtures...)
	for i, fixture := range plan.Fixtures {
		key := fixture.Workload + ":" + fixture.Name
		route := fixture.Workload + ":" + fixture.Integration + ":" + fixture.Method + ":" + fixture.Path
		if !known[fixture.Workload] || !simulationName.MatchString(fixture.Name) || !simulationIntegration.MatchString(fixture.Integration) || seen[key] || routes[route] {
			return nil, "", errors.New("simulation fixtures require unique names and routes in registered workloads")
		}
		seen[key], routes[route] = true, true
		if fixture.Adapter != "http" && fixture.Adapter != "resend" || !validSimulationPath(fixture.Path) || !validSimulationMethod(fixture.Method) {
			return nil, "", errors.New("simulation supports http or resend adapters with an exact method and path")
		}
		if fixture.Adapter == "resend" && (fixture.Method != http.MethodPost || fixture.Path != "/emails") {
			return nil, "", errors.New("resend simulation supports POST /emails")
		}
		if len(fixture.CaptureFields) > 32 || len(fixture.Responses) == 0 || len(fixture.Responses) > 16 {
			return nil, "", errors.New("simulation supports up to 32 capture fields and 1 to 16 responses per fixture")
		}
		fields := map[string]bool{}
		fixture.CaptureFields = append([]string(nil), fixture.CaptureFields...)
		for _, field := range fixture.CaptureFields {
			if !simulationField.MatchString(field) || fields[field] {
				return nil, "", errors.New("invalid simulation capture fields")
			}
			fields[field] = true
		}
		sort.Strings(fixture.CaptureFields)
		for _, response := range fixture.Responses {
			if err := validateSimulationResponse(response); err != nil {
				return nil, "", err
			}
		}
		plan.Fixtures[i] = fixture
	}
	sort.Slice(plan.Fixtures, func(i, j int) bool {
		return plan.Fixtures[i].Workload+":"+plan.Fixtures[i].Name < plan.Fixtures[j].Workload+":"+plan.Fixtures[j].Name
	})
	body, err := json.Marshal(plan)
	if err != nil || len(body) > MaxScenarioSimulationPlanBytes {
		return nil, "", errors.New("simulation fixture plan exceeds 64 KiB")
	}
	return body, ScenarioSimulationDigest(body), nil
}

func validSimulationPath(path string) bool {
	return strings.HasPrefix(path, "/") && len(path) <= 1024 && !strings.ContainsAny(path, "%?#\\\r\n\t ") && !strings.Contains(path, "//") && !strings.Contains(path, "/../") && !strings.Contains(path, "/./") && !strings.HasSuffix(path, "/..") && !strings.HasSuffix(path, "/.")
}

func validSimulationMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

func validateSimulationResponse(r ScenarioSimulationResponse) error {
	if r.DelayMS < 0 || r.DelayMS > 2000 || r.RetryAfterSeconds < 0 || r.RetryAfterSeconds > 30 || len(r.Body) > 8192 {
		return errors.New("simulation response exceeds protocol bounds")
	}
	if r.Fault != "" {
		if (r.Fault != "timeout" && r.Fault != "disconnect") || r.Status != 0 || r.Body != "" || r.RetryAfterSeconds != 0 || r.Fault == "timeout" && r.DelayMS == 0 {
			return errors.New("simulation fault requires timeout or disconnect, no HTTP response, and a bounded timeout delay")
		}
		return nil
	}
	if r.Status < 200 || r.Status > 599 || r.Status >= 300 && r.Status < 400 || r.RetryAfterSeconds != 0 && r.Status != 429 && r.Status != 503 {
		return errors.New("simulation requires a non-redirect HTTP response; Retry-After supports 429 or 503")
	}
	if r.Body != "" && !json.Valid([]byte(r.Body)) {
		return errors.New("simulation response body must be JSON")
	}
	if r.Body != "" {
		if _, err := behaviorcompare.CanonicalJSON([]byte(r.Body)); err != nil {
			return errors.New("simulation response body must be unambiguous JSON")
		}
	}
	return nil
}

// PrepareScenarioSimulation never performs I/O. The store assigns the ordinal
// under its run lock, persists this decision, then releases it to the handler.
func PrepareScenarioSimulation(plan ScenarioSimulationPlan, req ScenarioSimulationRequest, workload, id string, ordinal int) ScenarioSimulationDecision {
	a := ScenarioSimulationAttempt{ID: id, Workload: workload, Fixture: "unsupported", Integration: "unsupported", Adapter: "http", Method: "POST", Path: "/", RequestSHA256: ScenarioSimulationDigest(req.Body), Payload: json.RawMessage(`{}`), Attempt: ordinal, Status: "unsupported"}
	if req.IdempotencyKey != "" {
		a.IdempotencySHA256 = ScenarioSimulationDigest([]byte(req.IdempotencyKey))
	}
	d := ScenarioSimulationDecision{Attempt: a}
	if req.Invalid || len(req.Body) > MaxScenarioSimulationRequestBytes || len(req.IdempotencyKey) > 256 {
		return d
	}
	for _, f := range plan.Fixtures {
		if f.Workload != workload || f.Integration != req.Integration || f.Method != req.Method || f.Path != req.Path {
			continue
		}
		d.Attempt.Fixture, d.Attempt.Integration, d.Attempt.Adapter = f.Name, f.Integration, f.Adapter
		d.Attempt.Method, d.Attempt.Path = f.Method, f.Path
		body := req.Body
		if len(body) == 0 {
			body = []byte(`{}`)
		}
		canonical, err := behaviorcompare.CanonicalJSON(body)
		var fields map[string]json.RawMessage
		if err != nil || json.Unmarshal(canonical, &fields) != nil || fields == nil {
			return d
		}
		d.Attempt.RequestSHA256 = ScenarioSimulationDigest(canonical)
		if f.Adapter == "resend" && !validResendRequest(fields) {
			return d
		}
		capture := map[string]json.RawMessage{}
		for _, key := range f.CaptureFields {
			if value, ok := fields[key]; ok {
				capture[key] = value
			}
		}
		d.Attempt.Payload, _ = json.Marshal(capture)
		index := min(ordinal-1, len(f.Responses)-1)
		d.Response = f.Responses[index]
		d.Attempt.Status, d.Attempt.ResponseStatus, d.Attempt.Fault = "pending", d.Response.Status, d.Response.Fault
		return d
	}
	return d
}

func validResendRequest(fields map[string]json.RawMessage) bool {
	var from, subject string
	if json.Unmarshal(fields["from"], &from) != nil || from == "" || json.Unmarshal(fields["subject"], &subject) != nil || subject == "" {
		return false
	}
	var to string
	var recipients []string
	if json.Unmarshal(fields["to"], &to) != nil || to == "" {
		if json.Unmarshal(fields["to"], &recipients) != nil || len(recipients) == 0 {
			return false
		}
	}
	for _, key := range []string{"text", "html"} {
		var value string
		if json.Unmarshal(fields[key], &value) == nil && value != "" {
			return true
		}
	}
	return false
}

func DecodeScenarioSimulationPlan(body string) (ScenarioSimulationPlan, error) {
	var plan ScenarioSimulationPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return plan, fmt.Errorf("decode simulation fixtures: %w", err)
	}
	return plan, nil
}
