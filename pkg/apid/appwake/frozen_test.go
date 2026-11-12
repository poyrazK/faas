package appwake_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/appwake"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestResumableFrozenInstance(t *testing.T) {
	const dep = "dep-live"
	older := time.Date(2026, 11, 5, 11, 50, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	frozen := func(id, deployment string, lastRequest time.Time) state.Instance {
		return state.Instance{ID: id, DeploymentID: deployment, State: string(state.StateFrozen), LastRequestAt: lastRequest}
	}
	tests := []struct {
		name      string
		instances []state.Instance
		want      string
	}{
		{name: "none", want: ""},
		{name: "running only", instances: []state.Instance{{ID: "a", DeploymentID: dep, State: string(state.StateRunning)}}, want: ""},
		{name: "one frozen", instances: []state.Instance{frozen("a", dep, older)}, want: "a"},
		{name: "other deployment", instances: []state.Instance{frozen("a", "dep-old", newer)}, want: ""},
		{name: "fork", instances: []state.Instance{func() state.Instance { i := frozen("a", dep, newer); i.Mode = "fork"; return i }()}, want: ""},
		{name: "most recent request first", instances: []state.Instance{frozen("a", dep, older), frozen("b", dep, newer)}, want: "b"},
		{name: "tie breaks on lowest id", instances: []state.Instance{frozen("b", dep, older), frozen("a", dep, older)}, want: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := appwake.ResumableFrozenInstance(tt.instances, dep)
			if ok != (tt.want != "") || got.ID != tt.want {
				t.Fatalf("ResumableFrozenInstance = (%q, %v), want %q", got.ID, ok, tt.want)
			}
		})
	}
}
