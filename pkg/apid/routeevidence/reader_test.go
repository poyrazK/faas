package routeevidence_test

// adr: 1152

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeevidence"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeProm answers by metric name; a metric listed in fail returns an error.
type fakeProm struct {
	answers map[string][]promql.VectorSample
	fail    string
	queries []string
}

func (f *fakeProm) QueryVector(_ context.Context, query string) ([]promql.VectorSample, error) {
	f.queries = append(f.queries, query)
	for metric, samples := range f.answers {
		if strings.Contains(query, metric+"{") {
			if f.fail == metric {
				return nil, errors.New("prometheus down")
			}
			return samples, nil
		}
	}
	return nil, nil
}

type fakeApps map[string]state.App

func (f fakeApps) AppByID(_ context.Context, id string) (state.App, error) {
	if app, ok := f[id]; ok {
		return app, nil
	}
	return state.App{}, state.ErrNotFound
}

func sample(value float64, labels ...string) promql.VectorSample {
	m := map[string]string{}
	for i := 0; i+1 < len(labels); i += 2 {
		m[labels[i]] = labels[i+1]
	}
	return promql.VectorSample{Labels: m, Value: value}
}

const (
	acctID   = "acct-1"
	callerID = "11111111-1111-1111-1111-111111111111"
	authID   = "22222222-2222-2222-2222-222222222222"
	mailID   = "33333333-3333-3333-3333-333333333333"
	otherID  = "44444444-4444-4444-4444-444444444444"
)

func wakeAheadProm() *fakeProm {
	return &fakeProm{answers: map[string][]promql.VectorSample{
		"gateway_service_wake_ahead_edge_share": {
			sample(0.9, "target_app", authID, "kind", "call"),
			sample(0.8, "target_app", authID, "kind", "cold"),
			sample(0.4, "target_app", mailID, "kind", "call"),
			sample(0.9, "target_app", mailID, "kind", "cold"),
			sample(0.95, "target_app", otherID, "kind", "call"),
		},
		"gateway_service_wake_ahead_caller_wakes": {sample(40)},
		"gateway_service_wake_ahead_edge_total": {
			sample(12, "target_app", authID, "outcome", "started"),
			sample(10.4, "target_app", authID, "outcome", "used"),
			sample(1.6, "target_app", authID, "outcome", "unused"),
			sample(3, "target_app", otherID, "outcome", "started"),
		},
	}}
}

func apps() fakeApps {
	return fakeApps{
		authID:  {ID: authID, AccountID: acctID, Slug: "auth"},
		mailID:  {ID: mailID, AccountID: acctID, Slug: "mail"},
		otherID: {ID: otherID, AccountID: "acct-2", Slug: "foreign"},
	}
}

func TestWakeAheadLearnedShowsOwnTargetsOnly(t *testing.T) {
	prom := wakeAheadProm()
	r := routeevidence.Reader{Prom: prom, Apps: apps()}
	got := r.WakeAheadLearned(t.Context(), state.Account{ID: acctID}, state.App{ID: callerID})
	if got.Source != api.RouteEvidenceSourceLive || got.Window != "24h" || got.CallerWakes != 40 {
		t.Fatalf("learned = %+v", got)
	}
	if len(got.Targets) != 2 || got.Targets[0].Slug != "auth" || got.Targets[1].Slug != "mail" {
		t.Fatalf("targets = %+v; want auth then mail, foreign hidden", got.Targets)
	}
	auth, mail := got.Targets[0], got.Targets[1]
	if !auth.Qualifies || auth.Started != 12 || auth.Used != 10 || auth.Unused != 2 || auth.AppID != authID {
		t.Fatalf("auth = %+v", auth)
	}
	if mail.Qualifies {
		t.Fatalf("mail call share %.2f is below the threshold but qualifies", mail.CallShare)
	}
	for _, q := range prom.queries {
		if !strings.Contains(q, `caller_app="`+callerID+`"`) {
			t.Fatalf("query %q is not scoped to the caller", q)
		}
	}
}

func TestWakeAheadLearnedNeedsEnoughWakesToQualify(t *testing.T) {
	prom := wakeAheadProm()
	prom.answers["gateway_service_wake_ahead_caller_wakes"] = []promql.VectorSample{sample(float64(api.ServiceWakeAheadMinWakes - 1))}
	got := routeevidence.Reader{Prom: prom, Apps: apps()}.WakeAheadLearned(t.Context(), state.Account{ID: acctID}, state.App{ID: callerID})
	for _, target := range got.Targets {
		if target.Qualifies {
			t.Fatalf("%s qualifies with %d wakes", target.Slug, got.CallerWakes)
		}
	}
}

func TestEvidenceUnavailable(t *testing.T) {
	for name, r := range map[string]routeevidence.Reader{
		"no prometheus": {Apps: apps()},
		"query fails":   {Prom: &fakeProm{answers: wakeAheadProm().answers, fail: "gateway_service_wake_ahead_edge_total"}, Apps: apps()},
	} {
		t.Run(name, func(t *testing.T) {
			learned := r.WakeAheadLearned(t.Context(), state.Account{ID: acctID}, state.App{ID: callerID})
			if learned.Source != api.RouteEvidenceSourceUnavailable || learned.Targets == nil || len(learned.Targets) != 0 {
				t.Fatalf("learned = %+v", learned)
			}
		})
	}
	queue := routeevidence.Reader{}.PriorityQueue(t.Context(), state.App{ID: callerID})
	if queue.Source != api.RouteEvidenceSourceUnavailable || queue.Classes == nil || len(queue.Classes) != 0 {
		t.Fatalf("queue = %+v", queue)
	}
}

func TestPriorityQueueClasses(t *testing.T) {
	prom := &fakeProm{answers: map[string][]promql.VectorSample{
		"gateway_route_priority_queue_total": {
			sample(20, "class", "critical", "outcome", "queued"),
			sample(4, "class", "critical", "outcome", "displacing"),
			sample(9, "class", "normal", "outcome", "rejected"),
			sample(5.6, "class", "normal", "outcome", "displaced"),
			sample(7, "class", "unknown", "outcome", "queued"),
		},
	}}
	got := routeevidence.Reader{Prom: prom}.PriorityQueue(t.Context(), state.App{ID: callerID})
	want := []api.RoutePriorityQueueClass{
		{Class: "critical", Queued: 20, Displacing: 4},
		{Class: "normal", Displaced: 6, Rejected: 9},
		{Class: "bulk"},
	}
	if got.Source != api.RouteEvidenceSourceLive || len(got.Classes) != len(want) {
		t.Fatalf("queue = %+v", got)
	}
	for i := range want {
		if got.Classes[i] != want[i] {
			t.Fatalf("class %d = %+v, want %+v", i, got.Classes[i], want[i])
		}
	}
	if !strings.Contains(prom.queries[0], `app="`+callerID+`"`) || !strings.Contains(prom.queries[0], "[24h]") {
		t.Fatalf("query = %q", prom.queries[0])
	}
}
