package state

// adr: 456

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func TestRouteHealthHistoryKeyPinsEvidenceAndIgnoresRetryClock(t *testing.T) {
	now := time.Date(2026, 10, 2, 21, 10, 45, 0, time.UTC)
	anchor := now.Add(-time.Hour)
	d := Deployment{ID: uuid.NewString(), AppID: uuid.NewString(), CanaryTotalSteps: 4, TrafficPercent: 1}
	g := api.RouteHealthGate{AppID: d.AppID, Mode: "enforce", Revision: 1, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}
	d.CanaryStepStartedAt = &anchor
	r, _ := newRouteHealthReport(g, d, now)
	routehealth.Evaluate(&r, &anchor, "telemetry_unavailable")
	p := CanaryAdvanceParams{TrafficPercent: 10}
	_, first, err := buildRouteHealthHistory(r, d, p)
	if err != nil {
		t.Fatal(err)
	}
	r.CheckedAt = now.Add(time.Second)
	_, retry, err := buildRouteHealthHistory(r, d, p)
	if err != nil || first.Key != retry.Key || string(first.Body) == string(retry.Body) {
		t.Fatal("key includes retry wall time", err)
	}
	for _, change := range []string{"counts", "p95", "budget", "anchor", "revision", "source", "intent", "window"} {
		t.Run(change, func(t *testing.T) {
			var report api.RouteHealthReport
			body, _ := json.Marshal(r)
			_ = json.Unmarshal(body, &report)
			params := p
			switch change {
			case "counts":
				report.Routes[0].Windows[0].Candidate.Requests++
			case "p95":
				value := 120.0
				report.Routes[0].Windows[0].Candidate.P95LatencyMS = &value
			case "budget":
				report.Routes[0].MaxP95MS++
			case "anchor":
				*report.ObservationAnchor = anchor.Add(time.Second)
			case "revision":
				report.Revision++
			case "source":
				params.RequireSafeReleaseLease = true
			case "intent":
				params.TrafficPercent++
			case "window":
				report.Routes[0].Windows[0].Start = report.Routes[0].Windows[0].Start.Add(time.Minute)
			}
			_, changed, err := buildRouteHealthHistory(report, d, params)
			if err != nil || changed.Key == first.Key {
				t.Fatal("changed evidence deduplicated", err)
			}
		})
	}
	r.CandidateCommitSHA = strings.Repeat("x", api.RouteHealthHistoryEntryMaxBytes)
	if _, _, err := buildRouteHealthHistory(r, d, p); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("oversized entry accepted", err)
	}
}

func TestRouteHealthHistoryMemRetentionBounds(t *testing.T) {
	for _, size := range []int{1024, api.RouteHealthHistoryEntryMaxBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := NewMemStore()
			for i := range api.RouteHealthHistoryMaxEntries + 10 {
				m.appendRouteHealthHistoryLocked("dep", routeHealthStoredDecision{Key: fmt.Sprint(i), Body: []byte(strings.Repeat("x", size)), CheckedAt: time.Unix(int64(i), 0)})
			}
			entries := m.routeHealthHistory["dep"]
			want := min(api.RouteHealthHistoryMaxEntries, api.RouteHealthHistoryMaxBytes/size)
			if len(entries) != want || entries[len(entries)-1].Key != "109" {
				t.Fatal("retention exceeded bounds")
			}
			m.appendRouteHealthHistoryLocked("dep", entries[len(entries)-1])
			if len(m.routeHealthHistory["dep"]) != want {
				t.Fatal("duplicate grew history")
			}
		})
	}
}
