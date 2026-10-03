package routemonitor

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func monitorFixture() api.RouteMonitorReport {
	at := time.Date(2026, 10, 3, 12, 5, 45, 0, time.UTC)
	updated := at.Add(-time.Hour)
	budget := int64(500)
	r := NewReport(api.RouteMonitorConfig{AppID: uuid.NewString(), Enabled: true, Revision: 1, UpdatedAt: &updated, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget, MaxP95MS: 300}}}, at)
	r.DeploymentID = uuid.NewString()
	r.ObservationAnchor = &updated
	for j := range r.Routes[0].Windows {
		ms := 100.0
		r.Routes[0].Windows[j].Observed = api.RouteHealthCounts{Requests: 100, P95LatencyMS: &ms}
	}
	return r
}

// ADR-464: independent sustained signals, strict absolute budgets and sparse evidence.
func TestProductionRouteMonitorBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*api.RouteMonitorReport)
	}{
		{"healthy", "healthy", func(*api.RouteMonitorReport) {}},
		{"error equality allowed", "healthy", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				r.Routes[0].Windows[j].Observed.ServerErrors = 5
			}
		}},
		{"errors violate", "violated", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				r.Routes[0].Windows[j].Observed.ServerErrors = 6
			}
		}},
		{"latency equality allowed", "healthy", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				ms := 300.0
				r.Routes[0].Windows[j].Observed.P95LatencyMS = &ms
			}
		}},
		{"slow successful requests", "violated", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				ms := 300.1
				r.Routes[0].Windows[j].Observed.P95LatencyMS = &ms
			}
		}},
		{"sparse", "unknown", func(r *api.RouteMonitorReport) { r.Routes[0].Windows[0].Observed.Requests = 19 }},
		{"missing latency", "unknown", func(r *api.RouteMonitorReport) { r.Routes[0].Windows[0].Observed.P95LatencyMS = nil }},
		{"mixed signals", "unknown", func(r *api.RouteMonitorReport) {
			r.Routes[0].Windows[0].Observed.ServerErrors = 6
			ms := 301.0
			r.Routes[0].Windows[1].Observed.P95LatencyMS = &ms
		}},
		{"anchor reset", "unknown", func(r *api.RouteMonitorReport) { r.ObservationAnchor = &r.CheckedAt }},
		{"latency zero valid", "healthy", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				ms := 0.0
				r.Routes[0].Windows[j].Observed.P95LatencyMS = &ms
			}
		}},
		{"zero error budget selected", "violated", func(r *api.RouteMonitorReport) {
			r.Routes[0].Route.Max5xxRateBPS = new(int64)
			for j := range r.Routes[0].Windows {
				r.Routes[0].Windows[j].Observed.ServerErrors = 2
			}
		}},
		{"single error unknown", "unknown", func(r *api.RouteMonitorReport) {
			r.Routes[0].Route.Max5xxRateBPS = new(int64)
			for j := range r.Routes[0].Windows {
				r.Routes[0].Windows[j].Observed.ServerErrors = 1
			}
		}},
		{"disabled", "disabled", func(r *api.RouteMonitorReport) { r.Enabled = false }},
		{"large counts no overflow", "violated", func(r *api.RouteMonitorReport) {
			for j := range r.Routes[0].Windows {
				r.Routes[0].Windows[j].Observed.Requests = math.MaxInt64
				r.Routes[0].Windows[j].Observed.ServerErrors = math.MaxInt64 / 2
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := monitorFixture()
			tc.edit(&r)
			Evaluate(&r, "")
			if r.Status != tc.want {
				t.Fatalf("%s: %+v", tc.want, r)
			}
			if err := ValidateReport(r); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestProductionRouteMonitorConfigurationAndResponseValidation(t *testing.T) {
	budget := int64(500)
	base := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/profiles/{id}", Max5xxRateBPS: &budget}}}
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*api.SetRouteMonitorRequest)
	}{
		{"no budget", func(r *api.SetRouteMonitorRequest) { r.Routes[0].Max5xxRateBPS = nil }},
		{"missing revision", func(r *api.SetRouteMonitorRequest) { r.ExpectedRevision = nil }},
		{"negative rate", func(r *api.SetRouteMonitorRequest) { v := int64(-1); r.Routes[0].Max5xxRateBPS = &v }},
		{"rate above 100 percent", func(r *api.SetRouteMonitorRequest) {
			v := api.RouteMonitorMaxRateBPS + 1
			r.Routes[0].Max5xxRateBPS = &v
		}},
		{"wildcard", func(r *api.SetRouteMonitorRequest) { r.Routes[0].Path = "/profiles/*" }},
		{"duplicate route", func(r *api.SetRouteMonitorRequest) { r.Routes = append(r.Routes, r.Routes[0]) }},
		{"too many", func(r *api.SetRouteMonitorRequest) {
			r.Routes = make([]api.RouteMonitorRoute, api.RouteHealthMaxRoutes+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.Routes = CloneRoutes(base.Routes)
			tc.edit(&r)
			if Validate(r) == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, edit := range []func(*api.RouteMonitorReport){func(r *api.RouteMonitorReport) { r.Status = "violated" }, func(r *api.RouteMonitorReport) { r.Routes[0].Windows[0].Observed.ErrorRate = .99 }, func(r *api.RouteMonitorReport) { r.Routes[0].Windows[0].Start = r.CheckedAt }, func(r *api.RouteMonitorReport) { v := math.NaN(); r.Routes[0].Windows[0].Observed.P95LatencyMS = &v }, func(r *api.RouteMonitorReport) { r.DeploymentID = "" }} {
		r := monitorFixture()
		Evaluate(&r, "")
		edit(&r)
		if ValidateReport(r) == nil {
			t.Fatal("tampered response accepted")
		}
	}
	r := monitorFixture()
	Evaluate(&r, "telemetry_not_entitled")
	if ValidateReport(r) != nil || r.Status != "unknown" {
		t.Fatal("missing entitlement must remain unknown")
	}
	copy := CloneRoutes(base.Routes)
	*copy[0].Max5xxRateBPS = 1
	if *base.Routes[0].Max5xxRateBPS != 500 {
		t.Fatal("budget aliases caller memory")
	}
	if !strings.Contains(Validate(api.SetRouteMonitorRequest{}).Error(), "array") {
		t.Fatal("nil array accepted")
	}
}
