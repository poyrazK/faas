// Package routestatus joins every per-route protection into one read-only
// view: captured contract, observed traffic, canary route health, production
// budgets with automatic rollback, and saved requirements. It is pure; the
// CLI fetches each input independently and reports unavailable sections.
package routestatus

import (
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

const Version = 1

// Operation is one method/path pair from a captured OpenAPI document.
type Operation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operation_id,omitempty"`
}

// Inputs are the independently fetched reads. A nil input is unavailable;
// Unavailable explains why, keyed by section.
type Inputs struct {
	App                 string
	ServingDeploymentID string
	CandidateDeployment string
	Since               string
	Contract            []Operation
	ContractCaptured    bool
	Usage               *api.RouteCustomerUsageResponse
	HealthGate          *api.RouteHealthGate
	HealthReport        *api.RouteHealthReport
	Monitor             *api.RouteMonitorConfig
	MonitorReport       *api.RouteMonitorReport
	LatestIncident      *api.RouteMonitorIncident
	Requirements        *api.SavedRouteRequirements
	Unavailable         map[string]string
}

type Canary struct {
	Mode     string `json:"mode"`
	Status   string `json:"status,omitempty"`
	Evidence string `json:"evidence_window,omitempty"`
}

type Production struct {
	Max5xxRateBPS *int64 `json:"max_5xx_rate_bps,omitempty"`
	MaxP95MS      int64  `json:"max_p95_ms,omitempty"`
	OnViolation   string `json:"on_violation"`
	Status        string `json:"status,omitempty"`
	Evidence      string `json:"evidence_window,omitempty"`
}

type Route struct {
	Method       string      `json:"method"`
	Path         string      `json:"path"`
	ContractPath string      `json:"contract_path,omitempty"`
	OperationID  string      `json:"operation_id,omitempty"`
	InContract   bool        `json:"in_contract"`
	Requests     int64       `json:"requests"`
	Tenants      int64       `json:"tenants"`
	Consumers    int64       `json:"consumers"`
	LastSeen     string      `json:"last_seen,omitempty"`
	Requirements bool        `json:"requirements"`
	Canary       *Canary     `json:"canary,omitempty"`
	Production   *Production `json:"production,omitempty"`
	Protection   string      `json:"protection"`
	Gaps         []string    `json:"gaps"`
}

type Incident struct {
	ID       string                            `json:"id"`
	Status   string                            `json:"status"`
	OpenedAt string                            `json:"opened_at"`
	Rollback *api.RouteMonitorIncidentRollback `json:"rollback,omitempty"`
}

type Summary struct {
	Routes      int `json:"routes"`
	Protected   int `json:"protected"`
	Unprotected int `json:"unprotected_with_traffic"`
	Rollback    int `json:"rollback_armed"`
}

type Status struct {
	Version             int               `json:"version"`
	App                 string            `json:"app"`
	ServingDeploymentID string            `json:"serving_deployment_id,omitempty"`
	CandidateDeployment string            `json:"candidate_deployment_id,omitempty"`
	Since               string            `json:"since"`
	Coverage            string            `json:"coverage"`
	Summary             Summary           `json:"summary"`
	Routes              []Route           `json:"routes"`
	LatestIncident      *Incident         `json:"latest_incident,omitempty"`
	Unavailable         map[string]string `json:"unavailable,omitempty"`
}

// Gap identifiers are stable for JSON clients.
const (
	GapUnprotected        = "traffic_without_protection"
	GapOutsideContract    = "traffic_outside_contract"
	GapNoTraffic          = "contract_route_without_traffic"
	GapRollbackNoErrorCap = "rollback_needs_error_budget"
)

// OperationsFromDoc lists method/path pairs of a captured OpenAPI document.
func OperationsFromDoc(doc map[string]any) []Operation {
	paths, _ := doc["paths"].(map[string]any)
	out := []Operation{}
	for path, raw := range paths {
		item, _ := raw.(map[string]any)
		for method, op := range item {
			upper := strings.ToUpper(method)
			switch upper {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			default:
				continue
			}
			id := ""
			if fields, ok := op.(map[string]any); ok {
				id, _ = fields["operationId"].(string)
			}
			out = append(out, Operation{Method: upper, Path: path, OperationID: id})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// shape replaces every whole-segment path parameter so /users/{id} and
// /users/{userId} compare equal.
func shape(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			segments[i] = "{}"
		}
	}
	return strings.Join(segments, "/")
}

// Build joins the inputs. Rows are keyed by the gateway-normalized method and
// path; a contract operation joins an observed row by exact path first, then
// by parameter shape only when that shape is unique on both sides.
func Build(in Inputs) Status {
	s := Status{Version: Version, App: in.App, ServingDeploymentID: in.ServingDeploymentID, CandidateDeployment: in.CandidateDeployment, Since: in.Since, Coverage: "observed_only", Routes: []Route{}, Unavailable: in.Unavailable}
	rows := map[string]*Route{}
	order := []string{}
	row := func(method, path string) *Route {
		key := method + " " + path
		if r, ok := rows[key]; ok {
			return r
		}
		r := &Route{Method: method, Path: path, Gaps: []string{}}
		rows[key] = r
		order = append(order, key)
		return r
	}
	if in.Usage != nil {
		for _, u := range in.Usage.Routes {
			path, ok := strings.CutPrefix(u.Route, u.Method+" ")
			if !ok {
				continue
			}
			r := row(u.Method, path)
			r.Requests, r.Tenants, r.Consumers, r.LastSeen = u.Requests, u.PlatformTenantCount, u.ConsumerCount, u.LastObservedAt
		}
	}
	if in.HealthGate != nil {
		for _, g := range in.HealthGate.Routes {
			row(g.Method, g.Path).Canary = &Canary{Mode: in.HealthGate.Mode}
		}
		if in.HealthReport != nil {
			for _, f := range in.HealthReport.Routes {
				if r, ok := rows[f.Method+" "+f.Path]; ok && r.Canary != nil {
					r.Canary.Status, r.Canary.Evidence = f.Status, f.EvidenceWindow
				}
			}
		}
	}
	if in.Monitor != nil && in.Monitor.Enabled {
		action := routemonitor.OnViolation(in.Monitor.OnViolation)
		for _, m := range in.Monitor.Routes {
			row(m.Method, m.Path).Production = &Production{Max5xxRateBPS: m.Max5xxRateBPS, MaxP95MS: m.MaxP95MS, OnViolation: action}
		}
		if in.MonitorReport != nil {
			for _, f := range in.MonitorReport.Routes {
				if r, ok := rows[f.Route.Method+" "+f.Route.Path]; ok && r.Production != nil {
					r.Production.Status, r.Production.Evidence = f.Status, f.EvidenceWindow
				}
			}
		}
	}
	if in.Requirements != nil {
		for _, q := range in.Requirements.Requirements.Routes {
			row(strings.ToUpper(q.Method), q.Path).Requirements = true
		}
	}
	joinContract(in.Contract, rows, row)

	for _, key := range order {
		r := rows[key]
		classify(r, in)
		s.Routes = append(s.Routes, *r)
	}
	sort.SliceStable(s.Routes, func(i, j int) bool {
		a, b := s.Routes[i], s.Routes[j]
		if a.Tenants != b.Tenants {
			return a.Tenants > b.Tenants
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	for _, r := range s.Routes {
		s.Summary.Routes++
		// Saved requirements check policy, not release health.
		if r.Protection != "none" && r.Protection != "policy_only" {
			s.Summary.Protected++
		}
		if r.Protection == "rollback" {
			s.Summary.Rollback++
		}
		if hasGap(r, GapUnprotected) {
			s.Summary.Unprotected++
		}
	}
	if i := in.LatestIncident; i != nil {
		s.LatestIncident = &Incident{ID: i.ID, Status: i.Status, OpenedAt: i.OpenedAt.UTC().Format("2006-01-02T15:04:05Z"), Rollback: i.Rollback}
	}
	return s
}

func joinContract(ops []Operation, rows map[string]*Route, row func(string, string) *Route) {
	byShape := map[string][]*Route{}
	for _, r := range rows {
		byShape[r.Method+" "+shape(r.Path)] = append(byShape[r.Method+" "+shape(r.Path)], r)
	}
	opsByShape := map[string]int{}
	for _, op := range ops {
		opsByShape[op.Method+" "+shape(op.Path)]++
	}
	for _, op := range ops {
		if r, ok := rows[op.Method+" "+op.Path]; ok {
			r.InContract, r.OperationID = true, op.OperationID
			continue
		}
		key := op.Method + " " + shape(op.Path)
		if matches := byShape[key]; len(matches) == 1 && opsByShape[key] == 1 && !matches[0].InContract {
			matches[0].InContract, matches[0].ContractPath, matches[0].OperationID = true, op.Path, op.OperationID
			continue
		}
		r := row(op.Method, op.Path)
		r.InContract, r.OperationID = true, op.OperationID
	}
}

func classify(r *Route, in Inputs) {
	switch {
	case r.Production != nil && r.Production.OnViolation == "rollback" && r.Production.Max5xxRateBPS != nil:
		r.Protection = "rollback"
	case r.Canary != nil && r.Canary.Mode == "enforce":
		r.Protection = "enforced"
	case r.Canary != nil || r.Production != nil:
		r.Protection = "monitored"
	case r.Requirements:
		r.Protection = "policy_only"
	default:
		r.Protection = "none"
	}
	if r.Requests > 0 && r.Canary == nil && r.Production == nil {
		r.Gaps = append(r.Gaps, GapUnprotected)
	}
	if in.ContractCaptured && r.Requests > 0 && !r.InContract {
		r.Gaps = append(r.Gaps, GapOutsideContract)
	}
	if in.Usage != nil && r.InContract && r.Requests == 0 {
		r.Gaps = append(r.Gaps, GapNoTraffic)
	}
	if r.Production != nil && r.Production.OnViolation == "rollback" && r.Production.Max5xxRateBPS == nil {
		r.Gaps = append(r.Gaps, GapRollbackNoErrorCap)
	}
}

func hasGap(r Route, gap string) bool {
	for _, g := range r.Gaps {
		if g == gap {
			return true
		}
	}
	return false
}
