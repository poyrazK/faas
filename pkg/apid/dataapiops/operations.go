// Package dataapiops shapes bounded, credential-free owner observations.
package dataapiops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Report struct {
	State, Runtime, RuntimeState string
	Active, Queued, Capacity     int
	ObservedAt                   time.Time
	Checks                       []Check
	Sample                       *Sample
	Budgets                      *api.DataAPIConnectionBudgets
	Message                      string
}
type Check struct{ Name, State string }
type Request struct {
	ID      string
	Status  int
	Latency *int
	Time    time.Time
}
type Sample struct {
	Requests, RateLimited, Overloaded, Errors int
	P95                                       *int
	Partial                                   bool
	Recent                                    []Request
}

var runtimeVersion = regexp.MustCompile(`^[a-f0-9]{16}$`)
var requestID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func ValidRequestID(id string) bool { return requestID.MatchString(id) }

// Probe accepts only the platform-derived HTTPS origin; the caller must never
// supply a customer query parameter or a custom domain here.
func Probe(ctx context.Context, client *http.Client, address, current string) Report {
	out := Report{State: "unknown", RuntimeState: "unknown", ObservedAt: time.Now().UTC()}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/healthz", nil)
	if err != nil {
		return out
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return out
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 503 {
		return out
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.DataAPIHealthMaxBytes+1))
	if err != nil || len(body) > api.DataAPIHealthMaxBytes {
		return out
	}
	var p struct {
		Version   int                                     `json:"version"`
		Live      *bool                                   `json:"live"`
		Ready     *bool                                   `json:"ready"`
		Runtime   string                                  `json:"runtime_version"`
		Checks    map[string]string                       `json:"checks"`
		Admission struct{ Active, Queued, Capacity *int } `json:"admission"`
	}
	if json.Unmarshal(body, &p) != nil || p.Version != 1 || p.Live == nil || !*p.Live || p.Ready == nil || (*p.Ready) != (response.StatusCode == 200) {
		return out
	}
	for _, name := range []string{"engine", "sql", "database", "refresh"} {
		v := p.Checks[name]
		if !slices.Contains([]string{"ready", "unready", "reachable", "unreachable", "unavailable", "unknown", "paused", "disabled", "active"}, v) {
			return out
		}
		out.Checks = append(out.Checks, Check{name, v})
	}
	a := p.Admission
	if a.Active == nil || a.Queued == nil || a.Capacity == nil || *a.Capacity < 1 || *a.Capacity > api.DataAPIMaxPoolConnections || *a.Active < 0 || *a.Active > *a.Capacity || *a.Queued < 0 || *a.Queued > api.DataAPIMaxQueuedRequests {
		return Report{State: "unknown", RuntimeState: "unknown", ObservedAt: out.ObservedAt}
	}
	out.State = "ready"
	if !*p.Ready {
		out.State = "unready"
	}
	out.Active, out.Queued, out.Capacity = *a.Active, *a.Queued, *a.Capacity
	if runtimeVersion.MatchString(p.Runtime) {
		out.Runtime = p.Runtime
		if runtimeVersion.MatchString(current) {
			out.RuntimeState = "current"
			if p.Runtime != current {
				out.RuntimeState = "update available"
			}
		}
	}
	return out
}

// Summarize covers one retained edge-log page, not durable counters or SQL time.
func Summarize(events []state.LogEvent, partial bool) *Sample {
	out := &Sample{Partial: partial}
	latencies := []int{}
	for _, e := range events {
		if e.Source != state.LogEventSourceHTTP {
			continue
		}
		out.Requests++
		if e.Status == 429 {
			out.RateLimited++
		}
		if e.Status == 503 {
			out.Overloaded++
		}
		if e.Status >= 500 {
			out.Errors++
		}
		if e.LatencyMS != nil && *e.LatencyMS >= 0 {
			latencies = append(latencies, *e.LatencyMS)
		}
		if ValidRequestID(e.RequestID) {
			out.Recent = append(out.Recent, Request{e.RequestID, e.Status, e.LatencyMS, e.OccurredAt})
		}
	}
	if len(latencies) > 0 {
		slices.Sort(latencies)
		v := latencies[(95*len(latencies)+99)/100-1]
		out.P95 = &v
	}
	return out
}
