package apidserver_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type completenessReader struct {
	gaps        []string
	err         error
	calls       int
	from, until time.Time
}

func (c *completenessReader) RequestTelemetryCompleteness(_ context.Context, _ string, from, until time.Time) ([]string, error) {
	c.calls++
	c.from, c.until = from, until
	return c.gaps, c.err
}

// Losses (ring overwrites, exhausted retries, an app over its ingest cap)
// used to lower analytics totals without a trace. The response now says
// whether every gateway delivered the settled part of the window.
func TestRequestAnalyticsCompletenessJudgesSettledWindow(t *testing.T) {
	asOf := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	settled := asOf.Add(-api.RequestAnalyticsSettleLag)
	for _, tc := range []struct {
		name         string
		from, until  time.Time
		reader       completenessReader
		wantCalls    int
		wantUntil    time.Time
		wantComplete *bool
		wantGaps     []string
	}{
		{"live window is judged up to the settle lag", asOf.Add(-24 * time.Hour), asOf, completenessReader{gaps: []string{}}, 1, settled, completenessBool(true), []string{}},
		{"historical window is judged whole", asOf.Add(-48 * time.Hour), asOf.Add(-24 * time.Hour), completenessReader{gaps: []string{}}, 1, asOf.Add(-24 * time.Hour), completenessBool(true), []string{}},
		{"gaps make the window incomplete", asOf.Add(-time.Hour), asOf, completenessReader{gaps: []string{"telemetry_window_incomplete"}}, 1, settled, completenessBool(false), []string{"telemetry_window_incomplete"}},
		{"a window still settling is pending", asOf.Add(-time.Minute), asOf, completenessReader{}, 0, time.Time{}, completenessBool(false), []string{"telemetry_ingestion_pending"}},
		{"a failed check is unknown", asOf.Add(-time.Hour), asOf, completenessReader{err: errors.New("down")}, 1, settled, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := tc.reader
			complete, gaps := apidserver.RequestAnalyticsCompleteness(context.Background(), &reader, "app", tc.from, tc.until, asOf)
			if reader.calls != tc.wantCalls || (tc.wantCalls > 0 && (!reader.from.Equal(tc.from) || !reader.until.Equal(tc.wantUntil))) {
				t.Fatalf("judged %d times over [%s, %s), want %d over [%s, %s)", reader.calls, reader.from, reader.until, tc.wantCalls, tc.from, tc.wantUntil)
			}
			if !reflect.DeepEqual(complete, tc.wantComplete) || !reflect.DeepEqual(gaps, tc.wantGaps) {
				t.Fatalf("completeness = %v %v, want %v %v", completenessValue(complete), gaps, completenessValue(tc.wantComplete), tc.wantGaps)
			}
		})
	}
}

// Metrics and request analytics had no reviewed organization permission, so
// a company app's members other than its creator got 403 from the API, CLI
// and MCP while the dashboard showed them the same numbers. Every member who
// can read the app now reaches them as the creator does.
func TestCompanyAppAnalyticsFollowOrganizationRoles(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "obs-team", "Obs Team", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "obs-team-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	_, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	for _, path := range []string{
		"/v1/apps/obs-team-api/metrics",
		"/v1/apps/obs-team-api/analytics",
		"/v1/apps/obs-team-api/analytics/timeseries",
		"/v1/apps/obs-team-api/analytics/route-customers",
	} {
		creator := e.Do(t, http.MethodGet, path, nil, nil).Code
		if creator == http.StatusForbidden || creator == http.StatusNotFound {
			t.Fatalf("creator GET %s = %d", path, creator)
		}
		for name, headers := range map[string]map[string]string{"admin": asAdmin, "developer": asDeveloper, "viewer": asViewer} {
			if r := e.Do(t, http.MethodGet, path, nil, headers); r.Code != creator {
				t.Fatalf("%s GET %s = %d %s, want the creator's %d", name, path, r.Code, r.Body, creator)
			}
		}
	}
}

func completenessBool(v bool) *bool { return &v }

func completenessValue(v *bool) any {
	if v == nil {
		return nil
	}
	return *v
}

// The raw reads that add only sampled detail (deployment costs, route
// deployment splits, sampled CPU) used to fail the whole analytics response
// when they errored, and ran unbounded beside rollup-answered windows. They
// now leave the detail out and say so in sampled_measures.
func TestRequestAnalyticsSampledReadsAreBestEffort(t *testing.T) {
	failed := func(context.Context) (int, error) { return 0, errors.New("statement timeout") }
	if n, ok, err := apidserver.RequestAnalyticsSampledRead(t.Context(), false, failed); n != 0 || ok || err != nil {
		t.Fatalf("failed raw read = %d %v %v, want left out without an error", n, ok, err)
	}
	slow := func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	start := time.Now()
	if n, ok, err := apidserver.RequestAnalyticsSampledRead(t.Context(), true, slow); n != 0 || ok || err != nil {
		t.Fatalf("slow read beside rollups = %d %v %v, want left out without an error", n, ok, err)
	}
	if took := time.Since(start); took > api.RequestAnalyticsSampledMeasuresTimeout+2*time.Second {
		t.Fatalf("bounded read took %s, want about %s", took, api.RequestAnalyticsSampledMeasuresTimeout)
	}
	if n, ok, err := apidserver.RequestAnalyticsSampledRead(t.Context(), true, func(context.Context) (int, error) { return 7, nil }); n != 7 || !ok || err != nil {
		t.Fatalf("read = %d %v %v, want 7 included", n, ok, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := apidserver.RequestAnalyticsSampledRead(cancelled, false, failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("read for a cancelled request = %v, want the cancellation", err)
	}
	if apidserver.RequestAnalyticsSampledMeasuresState(true) != api.RequestAnalyticsSampledMeasuresIncluded ||
		apidserver.RequestAnalyticsSampledMeasuresState(false) != api.RequestAnalyticsSampledMeasuresOmitted {
		t.Fatal("sampled_measures states")
	}
}
