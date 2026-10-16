package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func recurrenceTestEpisode(now time.Time, rule, series string, minutes int, resolved *time.Time) state.OperatorAlertRecurrenceEpisode {
	e := state.OperatorAlertRecurrenceEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", Component: "apid", StartedAt: now.Add(time.Duration(minutes) * time.Minute), ResolvedAt: resolved, Status: "unknown"}
	e.ID = state.OperatorAlertEpisodeID(rule, series, e.StartedAt)
	if resolved != nil {
		e.Status = "resolved"
	}
	return e
}

func TestAlertRecurrencePartitionsAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	resolutions := []time.Time{now.Add(-9*time.Minute - 30*time.Second), now.Add(-8 * time.Minute), now.Add(-5*time.Minute - 30*time.Second), now.Add(-6*time.Minute - time.Microsecond), now.Add(time.Microsecond)}
	episodes := []state.OperatorAlertRecurrenceEpisode{
		recurrenceTestEpisode(now, rule, series, -10, &resolutions[0]),
		recurrenceTestEpisode(now, rule, series, -9, &resolutions[1]),
		recurrenceTestEpisode(now, rule, series, -8, nil),
		recurrenceTestEpisode(now, rule, series, -7, &resolutions[2]),
		recurrenceTestEpisode(now, rule, series, -6, &resolutions[3]),
		recurrenceTestEpisode(now, rule, series, -5, &resolutions[4]),
		recurrenceTestEpisode(now, rule, series, -4, nil),
		recurrenceTestEpisode(now, strings.Repeat("c", 64), series, -3, nil),
		recurrenceTestEpisode(now, rule, strings.Repeat("d", 64), -2, nil),
	}
	episodes[6].Status = "firing"
	reader := &recurrenceProbe{snapshot: state.OperatorAlertRecurrenceSnapshot{Collection: state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}, Episodes: episodes}}
	out, err := AlertRecurrence(t.Context(), reader, true, now.Add(-time.Hour), now)
	if err != nil || !reader.bounded || out.Totals == nil || out.Totals.Series != 3 || out.Totals.RepeatedSeries != 1 || out.Totals.Metrics.Episodes != 9 || out.Totals.Metrics.RepeatActivations != 6 || len(out.Items) != 3 || out.Truncated {
		t.Fatalf("recurrence: %+v %v", out, err)
	}
	item := out.Items[0]
	d, g := item.Metrics.Duration, item.Metrics.RecoveryGap
	if item.RuleKey != rule || item.SeriesID != series || item.Metrics.Episodes != 7 || item.Metrics.UnknownObservation != 1 || item.Metrics.Unresolved != 2 || d.Samples != 3 || d.NotObserved != 2 || d.InvalidClock != 2 || *d.MinSeconds != 30 || *d.P50Seconds != 60 || g.Samples != 2 || g.UnconfirmedRecovery != 1 || g.ResolutionOverlap != 1 || g.InvalidClock != 2 || *g.MinSeconds != 0 || *g.P50Seconds != 0 {
		t.Fatalf("evidence partitions: %+v", item)
	}
	if len(item.RecentEpisodes) != api.ObsAlertRecurrenceRecentMax || !item.RecentEpisodesTruncated || item.RecentEpisodes[0].EpisodeID != episodes[6].ID || item.RecentEpisodes[0].EvidencePath != EvidencePath("platform_alert", episodes[6].ID) || !item.FirstActivationAt.Equal(episodes[0].StartedAt) || !item.LastActivationAt.Equal(episodes[6].StartedAt) {
		t.Fatalf("episode preview: %+v", item)
	}
	for _, m := range []api.ObsAlertRecurrenceMetrics{item.Metrics, out.Totals.Metrics} {
		if m.Duration.Samples+m.Duration.NotObserved+m.Duration.InvalidClock != m.Episodes || m.RecoveryGap.Samples+m.RecoveryGap.UnconfirmedRecovery+m.RecoveryGap.ResolutionOverlap+m.RecoveryGap.InvalidClock != m.RepeatActivations {
			t.Fatal("partitions lost evidence")
		}
	}
	if out.Items[1].SeriesID != strings.Repeat("d", 64) || out.Items[1].Metrics.RecoveryGap.P50Seconds != nil || out.Items[2].Metrics.RepeatActivations != 0 || len(out.Limitations) == 0 {
		t.Fatalf("group isolation or ranking: %+v", out.Items)
	}
}

func TestAlertRecurrencePreviewTotalsAndTieOrder(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule := strings.Repeat("a", 64)
	reader := &recurrenceProbe{snapshot: state.OperatorAlertRecurrenceSnapshot{Collection: state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}}}
	for i := api.ObsAlertRecurrenceSeriesMax + 4; i >= 0; i-- {
		reader.snapshot.Episodes = append(reader.snapshot.Episodes, recurrenceTestEpisode(now, rule, fmt.Sprintf("%064x", i), -1, nil))
	}
	out, err := AlertRecurrence(t.Context(), reader, true, now.Add(-time.Hour), now)
	if err != nil || out.Totals.Series != 205 || out.Totals.Metrics.Episodes != 205 || !out.Truncated || len(out.Items) != api.ObsAlertRecurrenceSeriesMax || out.Items[0].SeriesID != fmt.Sprintf("%064x", 0) || out.Items[199].SeriesID != fmt.Sprintf("%064x", 199) {
		t.Fatalf("complete totals or deterministic preview: %+v %v", out, err)
	}
}

func TestAlertRecurrenceUnknownQuietWindowAndFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	since, err := RecurrenceSince("", now)
	if err != nil || !since.Equal(now.Add(-api.ObsAlertRecurrenceWindowDefault)) {
		t.Fatalf("default: %v %v", since, err)
	}
	if _, err := RecurrenceSince(now.Add(-api.ObsAlertRecurrenceWindowMax).Format(time.RFC3339Nano), now); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"bad", now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), now.Add(-api.ObsAlertRecurrenceWindowMax - time.Microsecond).Format(time.RFC3339Nano)} {
		if _, err := RecurrenceSince(raw, now); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid window: %s %v", raw, err)
		}
	}
	reader := &recurrenceProbe{}
	unknown, err := AlertRecurrence(t.Context(), reader, false, since, now)
	if err != nil || unknown.Totals != nil || unknown.Items == nil || unknown.Collection.CollectionState != "disabled" {
		t.Fatalf("unknown: %+v %v", unknown, err)
	}
	reader.snapshot.Collection = state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}
	quiet, err := AlertRecurrence(t.Context(), reader, true, since, now)
	if err != nil || quiet.Totals == nil || quiet.Totals.Series != 0 || quiet.Totals.Metrics.Duration.MinSeconds != nil || quiet.Totals.Metrics.RecoveryGap.P50Seconds != nil {
		t.Fatalf("quiet: %+v %v", quiet, err)
	}
	reader.err = context.DeadlineExceeded
	if _, err := AlertRecurrence(t.Context(), reader, true, since, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("failure: %v", err)
	}
	if _, err := AlertRecurrence(t.Context(), reader, true, now, now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid caller window: %v", err)
	}
}

type recurrenceProbe struct {
	snapshot state.OperatorAlertRecurrenceSnapshot
	err      error
	bounded  bool
}

func (p *recurrenceProbe) OperatorAlertRecurrence(ctx context.Context, _ state.OperatorAlertRecurrenceFilter) (state.OperatorAlertRecurrenceSnapshot, error) {
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= api.ObsAlertRecurrenceBudget
	return p.snapshot, p.err
}
