package schedule

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021152201 — what a due schedule does, for each state of its
// release and of the app's other releases.
func TestDecide(t *testing.T) {
	at := time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)
	sr := state.ScheduledRelease{ID: "s1", ReleaseAt: at}
	parked := state.Deployment{ID: "d12", Revision: 12, Status: state.DeployLive, TrafficPercentExplicit: true}
	serving := state.Deployment{ID: "d11", Revision: 11, Status: state.DeployLive, TrafficPercent: 100, RolloutState: "complete"}
	building := parked
	building.Status = state.DeployBuilding
	given := parked
	given.TrafficPercent = 100
	newer := state.Deployment{ID: "d13", Revision: 13, Status: state.DeployLive, TrafficPercent: 100}
	olderCanary := state.Deployment{ID: "d10", Revision: 10, Status: state.DeployLive, TrafficPercent: 10, CanaryTotalSteps: 3, RolloutState: "rolling_out"}
	failed := parked
	failed.Status = state.DeployFailed
	superseded := parked
	superseded.Status = state.DeploySuperseded
	for _, tc := range []struct {
		name     string
		d        state.Deployment
		siblings []state.Deployment
		now      time.Time
		action   string
		state    string
		code     string
	}{
		{"parked release starts", parked, []state.Deployment{serving}, at, ActionStart, "", ""},
		{"first release starts", parked, nil, at, ActionStart, "", ""},
		{"build still running waits", building, []state.Deployment{serving}, at.Add(time.Minute), ActionWait, "", ""},
		{"build still running after the grace expires", building, []state.Deployment{serving}, at.Add(api.ScheduledReleaseStartGrace), ActionEnd, state.ScheduledReleaseExpired, CodeNotReady},
		{"failed build cancels", failed, nil, at, ActionEnd, state.ScheduledReleaseCancelled, CodeDeploymentFailed},
		{"replaced release is superseded", superseded, nil, at, ActionEnd, state.ScheduledReleaseSuperseded, CodeSuperseded},
		{"traffic given by hand cancels", given, []state.Deployment{serving}, at, ActionEnd, state.ScheduledReleaseCancelled, CodeReleasedByHand},
		{"newer serving release supersedes", parked, []state.Deployment{newer}, at, ActionEnd, state.ScheduledReleaseSuperseded, CodeSuperseded},
		{"older canary in flight waits", parked, []state.Deployment{serving, olderCanary}, at, ActionWait, "", ""},
		{"older canary still in flight after the grace", parked, []state.Deployment{serving, olderCanary}, at.Add(2 * time.Hour), ActionEnd, state.ScheduledReleaseExpired, CodeNotReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(sr, tc.d, tc.siblings, tc.now)
			if got.Action != tc.action || got.State != tc.state || got.Code != tc.code {
				t.Fatalf("Decide = %+v, want %s %s %s", got, tc.action, tc.state, tc.code)
			}
			if got.Action == ActionEnd && got.Detail == "" {
				t.Fatal("an ended schedule must say why")
			}
		})
	}
}

func TestLadder(t *testing.T) {
	for _, spec := range []*api.CanaryPresetSpec{nil, {Preset: "none"}, {}} {
		if ladder, p := Ladder(spec); p != nil || ladder.TotalSteps != 0 {
			t.Fatalf("Ladder(%+v) = %+v, %v; want all traffic at once", spec, ladder, p)
		}
	}
	ladder, p := Ladder(&api.CanaryPresetSpec{Preset: "balanced"})
	if p != nil || ladder.Preset != "balanced" || ladder.TotalSteps < 2 || ladder.FirstPercent <= 0 || ladder.FirstPercent >= 100 || len(ladder.Stages) != 0 {
		t.Fatalf("balanced ladder = %+v, %v", ladder, p)
	}
	if _, p := Ladder(&api.CanaryPresetSpec{Preset: "nope"}); p == nil || p.Code != api.CodeInvalidCanaryPreset {
		t.Fatalf("unknown preset problem = %+v", p)
	}
	raw := json.RawMessage(`{"preset":"custom","stages":[{"percent":20,"duration":"5m"},{"percent":100,"duration":"0s"}]}`)
	spec, err := Spec(raw)
	if err != nil {
		t.Fatal(err)
	}
	custom, p := Ladder(spec)
	if p != nil || custom.Preset != "custom" || custom.TotalSteps != 2 || custom.FirstPercent != 20 || len(custom.Stages) == 0 {
		t.Fatalf("custom ladder = %+v, %v", custom, p)
	}
	if again, err := Spec(Encode(spec)); err != nil || again.Preset != "custom" || len(again.Stages) != 2 {
		t.Fatalf("round trip = %+v, %v", again, err)
	}
	if spec, err := Spec(nil); spec != nil || err != nil {
		t.Fatalf("Spec(nil) = %+v, %v", spec, err)
	}
}

func TestUnoverriddenFreezes(t *testing.T) {
	active := []state.ReleaseFreeze{{ID: "f1"}, {ID: "f2"}}
	if got := UnoverriddenFreezes(active, []string{"f1", "f2"}); len(got) != 0 {
		t.Fatalf("covered freezes still block: %+v", got)
	}
	// A freeze created after the schedule was not overridden by it.
	if got := UnoverriddenFreezes(active, []string{"f1"}); len(got) != 1 || got[0].ID != "f2" {
		t.Fatalf("new freeze = %+v", got)
	}
}

func TestClipDetail(t *testing.T) {
	long := strings.Repeat("é", 400)
	clipped := ClipDetail(long)
	if len(clipped) > 500 || !strings.HasSuffix(clipped, "...") || !strings.HasPrefix(long, strings.TrimSuffix(clipped, "...")) {
		t.Fatalf("clipped to %d bytes: %q", len(clipped), clipped[len(clipped)-8:])
	}
	if ClipDetail("short") != "short" {
		t.Fatal("short detail changed")
	}
}

func TestViewAndNotice(t *testing.T) {
	at := time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)
	started := at.Add(10 * time.Second)
	sr := state.ScheduledRelease{
		ID: "s1", DeploymentID: "d12", Revision: 12, ReleaseAt: at, State: state.ScheduledReleaseStarted,
		Canary: Encode(&api.CanaryPresetSpec{Preset: "balanced"}), ScheduledBy: "ops@example.com", StartedAt: &started,
		CreatedAt: at.Add(-time.Hour), UpdatedAt: started,
	}
	view := View(sr, "shop", nil)
	if view.App != "shop" || view.Revision != 12 || view.ReleaseAt != "2026-10-22T02:00:00Z" || view.Canary == nil ||
		view.Canary.Preset != "balanced" || view.StartedAt == "" || view.NotifyChannels == nil || view.OverriddenFreezes != nil {
		t.Fatalf("view = %+v", view)
	}
	app := state.App{Slug: "shop"}
	d := state.Deployment{ID: "d12", Revision: 12, CommitSHA: strings.Repeat("c", 40)}
	notice := Notice(alertchannels.EventScheduleBlocked, app, d, sr, "frozen for the launch", "https://gregale.dev/dashboard/releases/d12/approval")
	if notice.Event != alertchannels.EventScheduleBlocked || notice.Rollout != "balanced canary" || notice.Detail != "frozen for the launch" ||
		notice.ReleaseAt != "2026-10-22T02:00:00Z" || len(notice.Commit) != 12 {
		t.Fatalf("notice = %+v", notice)
	}
}

func TestRolloutSummary(t *testing.T) {
	if got := RolloutSummary(nil); got != "all traffic at once" {
		t.Fatalf("nil = %q", got)
	}
	if got := RolloutSummary(&api.CanaryPresetSpec{Preset: "adaptive"}); got != "adaptive canary" {
		t.Fatalf("adaptive = %q", got)
	}
}
