package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestEffectiveMinInstances(t *testing.T) {
	cases := []struct {
		name string
		app  *state.App
		want int
	}{
		{
			name: "nil app",
			app:  nil,
			want: 0,
		},
		{
			name: "both zero",
			app:  &state.App{MinInstances: 0},
			want: 0,
		},
		{
			name: "column only",
			app:  &state.App{MinInstances: 3},
			want: 3,
		},
		{
			name: "policy only — nil pointer",
			app:  &state.App{MinInstances: 0},
			want: 0,
		},
		{
			name: "policy only — set",
			app:  &state.App{MinInstances: 0, ScalingPolicy: &state.ScalingPolicy{MinInstances: 5}},
			want: 5,
		},
		{
			name: "policy wins over column",
			app:  &state.App{MinInstances: 3, ScalingPolicy: &state.ScalingPolicy{MinInstances: 5}},
			want: 5,
		},
		{
			name: "column wins over policy",
			app:  &state.App{MinInstances: 5, ScalingPolicy: &state.ScalingPolicy{MinInstances: 3}},
			want: 5,
		},
		{
			name: "equal values",
			app:  &state.App{MinInstances: 2, ScalingPolicy: &state.ScalingPolicy{MinInstances: 2}},
			want: 2,
		},
		{
			name: "function form nil-safe",
			app:  nil,
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := state.EffectiveMinInstances(c.app, time.Now()); got != c.want {
				t.Errorf("effectiveMinInstances(%+v) = %d, want %d", c.app, got, c.want)
			}
			if got := c.app.EffectiveMinInstances(); got != c.want {
				t.Errorf("(*App).EffectiveMinInstances() = %d, want %d", got, c.want)
			}
		})
	}
}
