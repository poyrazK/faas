//go:build !no_pg

package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueImpactAlertsRespectIgnorePostgres(t *testing.T) {
	for _, mode := range []string{"indefinite", "timed", "late-attribution", "expired"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanHobby)
			app := apidserver.SeedPGApp(t, e, "impact-ignore")
			dep := apidserver.IssueSeedDeployment(t, e, app, 1)
			token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
			base := "/v1/apps/" + app.Slug
			apidserver.IssueDecode[api.IssueImpactAlertPolicy](t, e.Do(t, http.MethodPut, base+"/issue-impact-alert-policy", api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: 1}, nil), 200)
			store := apidserver.IssueNativeStore(e)
			hook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: e.Acct().ID, AppID: app.ID, TargetURL: "https://example.com/impact", Enabled: true, EventFilter: []string{"issue.impact_threshold_reached"}, SecretSealed: []byte("test-sealed")})
			if err != nil {
				t.Fatal(err)
			}
			event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "IgnoredImpactError", Message: "impact collection continues"}
			first := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
			detailURL := base + "/issues/" + first.IssueID
			until := time.Now().UTC().Add(time.Hour)
			action := api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until}
			apidserver.IssueDecode[api.Issue](t, e.Do(t, http.MethodPost, detailURL+"/actions", action, nil), 200)
			if mode == "indefinite" {
				// The public action requires a deadline; tolerate legacy stored rows without one.
				if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET ignored_until=NULL WHERE id=$1", first.IssueID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "expired" {
				// Leave the stored state ignored to exercise expiry before maintenance.
				if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET ignored_until=now()-interval '1 second' WHERE id=$1", first.IssueID); err != nil {
					t.Fatal(err)
				}
			}
			request := uuid.NewString()
			if mode != "late-attribution" {
				apidserver.IssueSeedRequestAttribution(t, e, app, dep, request)
			}
			event.EventID = uuid.NewString()
			event.OccurredAt = time.Now().UTC()
			event.RequestID = request
			apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
			if mode == "late-attribution" {
				apidserver.IssueSeedRequestAttribution(t, e, app, dep, request)
				if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_events SET attribution_checked_at=now()-interval '2 minutes' WHERE app_id=$1 AND event_id=$2", app.ID, event.EventID); err != nil {
					t.Fatal(err)
				}
				maintenance := store.(interface {
					MaintainIssues(context.Context, time.Time) error
				})
				if err := maintenance.MaintainIssues(t.Context(), time.Now().UTC().Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, http.MethodGet, detailURL, nil, nil), 200)
			if detail.Impact.IdentifiedCustomers != 1 || detail.Impact.ObservedEvents != 2 {
				t.Fatalf("impact was lost while ignored: %+v", detail.Impact)
			}
			want := 0
			if mode == "expired" {
				want = 1
			}
			alerts := 0
			for _, activity := range detail.Activity {
				if activity.Action == "impact_threshold_reached" {
					alerts++
				}
			}
			if alerts != want {
				t.Fatalf("threshold activity=%d want=%d", alerts, want)
			}
			relay := store.(state.AppWebhookEventOutboxStore)
			if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil || n != want {
				t.Fatalf("relay=%d want=%d err=%v", n, want, err)
			}
			deliveries, _, err := store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
			if err != nil || len(deliveries) != want {
				t.Fatalf("deliveries=%+v want=%d err=%v", deliveries, want, err)
			}
			// Reopening does not replay the suppressed crossing. A fresh issue can
			// still alert: ignore is local to one issue, not an app-wide mute.
			apidserver.IssueDecode[api.Issue](t, e.Do(t, http.MethodPost, detailURL+"/actions", api.IssueActionRequest{Action: "reopen"}, nil), 200)
			event.EventID = uuid.NewString()
			event.OccurredAt = time.Now().UTC()
			apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
			if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil || n != 0 {
				t.Fatalf("reopen replay=%d err=%v", n, err)
			}
			event.EventID = uuid.NewString()
			event.ExceptionType = "OtherImpactError"
			event.FingerprintOverride = "other-impact-issue"
			apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
			if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil || n != 1 {
				t.Fatalf("independent crossing=%d err=%v", n, err)
			}
		})
	}
}
