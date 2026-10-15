package operatorobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestContextCursorAndTraceSafety(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	a, err := s.CreateAccount(ctx, "context-domain@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "context-domain", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployFailed})
	if err != nil {
		t.Fatal(err)
	}
	validTrace := strings.Repeat("a", 32)
	for _, trace := range []*string{&validTrace} {
		if err := s.AppendEventWithTrace(ctx, "private-actor", "deployment.failed", &d.ID, []byte(`{"private":"payload"}`), trace); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	first, err := Context(ctx, s, "deployment", d.ID, "", 1, 100, now)
	if err != nil || first.NextCursor == "" || first.Counts == nil || first.Counts.Apps != 1 || first.EvidencePath != EvidencePath("deployment", d.ID) {
		t.Fatalf("context=%+v err=%v", first, err)
	}
	for _, e := range first.Timeline {
		if e.Entry.TraceID == "" && e.Entry.TracePath != "" {
			t.Fatal("invalid trace escaped")
		}
		if e.Entry.TraceID == validTrace && e.Entry.TracePath != "/v1/admin/obs/traces/"+validTrace {
			t.Fatal("missing validated trace link")
		}
	}
	second, err := Context(ctx, s, "deployment", d.ID, first.NextCursor, 1, 100, now.Add(time.Second))
	if err != nil || !second.Until.Equal(first.Until) || !second.Since.Equal(first.Since) || !second.AsOf.After(first.AsOf) || second.Resources[0].ID == first.Resources[0].ID {
		t.Fatalf("cursor=%+v err=%v", second, err)
	}
	for _, raw := range []string{"garbage", cursor.Encode(cursor.Key{CreatedAt: now, ID: "deployment/" + uuid.NewString() + "|app/" + app.ID}), cursor.Encode(cursor.Key{CreatedAt: now, ID: first.IncidentID + "|"}), cursor.Encode(cursor.Key{CreatedAt: now.Add(-time.Hour), ID: first.IncidentID + "|app/" + app.ID})} {
		if _, err := Context(ctx, s, "deployment", d.ID, raw, 1, 10, now); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid cursor=%v", err)
		}
	}
}

func TestContextAlertRetainedDuringOutageAndEpisodeCursor(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a, err := s.CreateAccount(ctx, "context-alert@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "context-alert", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	key, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	seed := func(at time.Time) state.OperatorAlertEpisode {
		t.Helper()
		e := state.OperatorAlertEpisode{RuleKey: key, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: at, AppID: uuid.MustParse(app.ID).String(), AccountID: uuid.MustParse(a.ID).String()}
		e.ID = state.OperatorAlertEpisodeID(key, series, at)
		if err := s.RecordOperatorAlertSnapshot(ctx, state.OperatorAlertSnapshot{ObservedAt: at, Rules: []state.OperatorAlertRuleObservation{{Key: key, EvaluatedAt: at, EpisodeIDs: []string{e.ID}}}, Episodes: []state.OperatorAlertEpisode{e}}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := seed(now)
	if err := s.RecordOperatorAlertFailure(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out, err := Context(ctx, s, "platform_alert", series, "", 1, 10, now.Add(2*time.Second))
	if err != nil || out.IncidentID != "platform_alert/"+first.ID || out.IncidentStatus != "unknown" || out.Scope.State != "known" || out.Counts.Apps != 1 || out.NextCursor == "" || len(out.UnavailableSources) != 1 {
		t.Fatalf("outage=%+v err=%v", out, err)
	}
	seed(now.Add(3 * time.Second))
	if _, err := Context(ctx, s, "platform_alert", series, out.NextCursor, 1, 10, now.Add(4*time.Second)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("legacy cursor crossed episodes: %v", err)
	}
	old, err := Context(ctx, s, "platform_alert", first.ID, out.NextCursor, 1, 10, now.Add(4*time.Second))
	if err != nil || old.IncidentStatus != "resolved" {
		t.Fatalf("immutable episode cursor=%+v err=%v", old, err)
	}
}
