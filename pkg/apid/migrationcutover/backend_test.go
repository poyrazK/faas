package migrationcutover

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixture(t *testing.T) (Backend, *state.MemStore, *api.MigrationVerification, *state.CustomDomain, api.CreateMigrationCutoverRequest) {
	t.Helper()
	m := state.NewMemStore()
	a, err := m.CreateAccount(t.Context(), "cutover@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "cutover", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	v := &api.MigrationVerification{ID: uuid.NewString(), App: app.Slug, DeploymentID: uuid.NewString(), ConfigurationRevision: "revision", Scope: "default", Status: "passed", Request: api.CreateMigrationVerificationRequest{SmokePath: "/ready"}}
	d := &state.CustomDomain{Domain: "api.example.test", AppID: app.ID, ChallengeToken: "binding-one", VerifiedAt: time.Now()}
	b := Backend{Store: m, AccountID: a.ID, AppID: app.ID, App: app.Slug, Verification: func(context.Context, string) (api.MigrationVerification, error) { return *v, nil }, Domain: func(context.Context, string) (state.CustomDomain, error) { return *d, nil }, Instructions: func(d state.CustomDomain) []api.DNSRecordInstruction {
		return []api.DNSRecordInstruction{{Type: "TXT", Name: "_gregale." + d.Domain, Value: d.ChallengeToken}}
	}, Probe: func(context.Context, api.MigrationCutover) ([]api.MigrationVerificationCheck, error) {
		return []api.MigrationVerificationCheck{{Name: "deployment_route", Status: "passed", Detail: "Proven"}}, nil
	}, Traffic: func(context.Context, api.MigrationCutover) api.MigrationCutoverTraffic {
		return limitedTraffic("insufficient_requests")
	}}
	in := api.CreateMigrationCutoverRequest{ID: uuid.NewString(), VerificationID: v.ID, Hostname: d.Domain, PreviousRecords: []api.MigrationDNSRecord{{Type: "CNAME", Value: "old.example.test", TTL: 300}}, PreviousProxyEnabled: true, Max5xxRateBPS: 100, MaxP95MS: 1000}
	return b, m, v, d, in
}

func TestPrepareDoesNotProbeAndSurvivesBackendRestart(t *testing.T) {
	b, m, _, d, in := fixture(t)
	b.Probe = func(context.Context, api.MigrationCutover) ([]api.MigrationVerificationCheck, error) {
		t.Fatal("prepare initiated network work")
		return nil, nil
	}
	r, err := b.Create(t.Context(), in)
	if err != nil || r.Status != "prepared" {
		t.Fatalf("prepare: %+v %v", r, err)
	}
	due, err := m.ListDueMigrationCutovers(t.Context(), time.Now(), 16)
	if err != nil || len(due) != 0 {
		t.Fatalf("prepared work queued: %v %v", due, err)
	}
	restarted := b
	r, err = restarted.Get(t.Context(), in.ID)
	if err != nil || r.Request.PreviousRecords[0].Value != "old.example.test" || !r.Request.PreviousProxyEnabled {
		t.Fatalf("lost recovery: %+v %v", r, err)
	}
	r.Request.PreviousRecords[0].Value = "mutated.test"
	replayed, err := b.Create(t.Context(), in)
	if err != nil || replayed.Request.PreviousRecords[0].Value != "old.example.test" {
		t.Fatalf("replay mutated receipt: %+v %v", replayed, err)
	}
	changed := in
	changed.Hostname = "other.example.test"
	if _, err = b.Create(t.Context(), changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("identity reused: %v", err)
	}
	d.ChallengeToken = "recreated-domain"
	r, err = b.Get(t.Context(), in.ID)
	if err != nil || r.Status != "blocked" || r.Reason != "domain_binding_changed" || r.Request.PreviousRecords[0].Value != "old.example.test" {
		t.Fatalf("recreated domain accepted or recovery lost: %+v %v", r, err)
	}
}

func TestCompletionRequiresCurrentProofAndExplicitLimitedTrafficConsent(t *testing.T) {
	b, _, v, _, in := fixture(t)
	if _, err := b.Create(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "start_checks"}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Store: b.Store, Backend: func(state.MigrationCutoverRecord) Backend { return b }}
	if err := worker.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := b.Get(t.Context(), in.ID)
	if err != nil || r.Status != "ready" || r.HostnameVerifiedAt == nil {
		t.Fatalf("worker did not prove hostname: %+v %v", r, err)
	}
	if _, err = b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "complete"}); err == nil {
		t.Fatal("limited traffic silently accepted")
	}
	b.Traffic = func(context.Context, api.MigrationCutover) api.MigrationCutoverTraffic {
		return api.MigrationCutoverTraffic{Status: "violated"}
	}
	if _, err = b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "complete", AcknowledgeLimitedTraffic: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("violated budget bypassed: %v", err)
	}
	b.Traffic = func(context.Context, api.MigrationCutover) api.MigrationCutoverTraffic {
		return limitedTraffic("insufficient_requests")
	}
	r, err = b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "complete", AcknowledgeLimitedTraffic: true})
	if err != nil || r.Status != "completed" || !r.LimitedTrafficAcknowledged {
		t.Fatalf("completion: %+v %v", r, err)
	}
	v.ConfigurationRevision = "changed"
	r, err = b.Get(t.Context(), in.ID)
	if err != nil || r.Status != "blocked" {
		t.Fatalf("completed evidence remained current: %+v %v", r, err)
	}
	// Recovery is an explicit user attestation and remains possible after drift.
	r, err = b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "confirm_restored"})
	if err != nil || r.Status != "restored" || r.Request.PreviousRecords[0].TTL != 300 {
		t.Fatalf("recovery: %+v %v", r, err)
	}
}

func TestCancellationFencesLateProbeAndDeploymentChangesDuringProbe(t *testing.T) {
	for _, cancelRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "deployment_drift"}[cancelRun], func(t *testing.T) {
			b, _, v, _, in := fixture(t)
			if _, err := b.Create(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "start_checks"}); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			b.Probe = func(context.Context, api.MigrationCutover) ([]api.MigrationVerificationCheck, error) {
				close(entered)
				<-release
				return []api.MigrationVerificationCheck{{Status: "passed"}}, nil
			}
			var wg sync.WaitGroup
			wg.Go(func() {
				if _, err := b.Advance(t.Context(), in.ID); err != nil {
					t.Error(err)
				}
			})
			<-entered
			if cancelRun {
				if _, err := b.Act(t.Context(), in.ID, api.MigrationCutoverActionRequest{Action: "cancel"}); err != nil {
					t.Fatal(err)
				}
			} else {
				v.ConfigurationRevision = "changed"
			}
			close(release)
			wg.Wait()
			r, err := b.Get(t.Context(), in.ID)
			want := "blocked"
			if cancelRun {
				want = "cancelled"
			}
			if err != nil || r.Status != want {
				t.Fatalf("late evidence won: %+v %v", r, err)
			}
		})
	}
}

func TestTrafficExcludesPreSwitchWindowsAndCannotTreatNoTrafficAsHealthy(t *testing.T) {
	b, _, _, _, in := fixture(t)
	anchor := time.Now().Add(-10 * time.Minute).UTC()
	plan := api.MigrationCutover{DeploymentID: uuid.NewString(), SmokePath: "/ready", Request: in, HostnameVerifiedAt: &anchor}
	budget := in.Max5xxRateBPS
	r := api.RouteMonitorReport{AppID: b.AppID, DeploymentID: plan.DeploymentID, CheckedAt: time.Now(), Enabled: true, Routes: []api.RouteMonitorFinding{{Route: api.RouteMonitorRoute{Method: "GET", Path: "/ready", Max5xxRateBPS: &budget, MaxP95MS: 1000}, Windows: []api.RouteMonitorWindow{}}}}
	p95 := 100.0
	for _, w := range routehealth.Windows(time.Now()) {
		r.Routes[0].Windows = append(r.Routes[0].Windows, api.RouteMonitorWindow{Start: w.Start, End: w.End, Observed: api.RouteHealthCounts{Requests: 1000, P95LatencyMS: &p95}})
	}
	preview := api.RouteMonitorPreview{PreviewOnly: true, Report: r}
	if got := Traffic(preview, plan, b.AppID); got.Status != "healthy" {
		t.Fatalf("fresh observations rejected: %+v", got)
	}
	newAnchor := time.Now()
	plan.HostnameVerifiedAt = &newAnchor
	if got := Traffic(preview, plan, b.AppID); got.Status != "unknown" || got.Requests != 0 {
		t.Fatalf("staging traffic accepted: %+v", got)
	}
	plan.HostnameVerifiedAt = &anchor
	for i := range preview.Report.Routes[0].Windows {
		preview.Report.Routes[0].Windows[i].Observed = api.RouteHealthCounts{}
	}
	if got := Traffic(preview, plan, b.AppID); got.Status != "unknown" {
		t.Fatalf("zero traffic passed: %+v", got)
	}
}
