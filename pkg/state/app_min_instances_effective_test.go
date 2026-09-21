package state

import (
	"testing"
	"time"
)

func TestEffectiveMinInstances(t *testing.T) {
	cases := []struct {
		name string
		app  *App
		want int
	}{
		{
			name: "nil app",
			app:  nil,
			want: 0,
		},
		{
			name: "both zero",
			app:  &App{MinInstances: 0},
			want: 0,
		},
		{
			name: "column only",
			app:  &App{MinInstances: 3},
			want: 3,
		},
		{
			name: "policy only — nil pointer",
			app:  &App{MinInstances: 0},
			want: 0,
		},
		{
			name: "policy only — set",
			app:  &App{MinInstances: 0, ScalingPolicy: &ScalingPolicy{MinInstances: 5}},
			want: 5,
		},
		{
			name: "policy wins over column",
			app:  &App{MinInstances: 3, ScalingPolicy: &ScalingPolicy{MinInstances: 5}},
			want: 5,
		},
		{
			name: "column wins over policy",
			app:  &App{MinInstances: 5, ScalingPolicy: &ScalingPolicy{MinInstances: 3}},
			want: 5,
		},
		{
			name: "equal values",
			app:  &App{MinInstances: 2, ScalingPolicy: &ScalingPolicy{MinInstances: 2}},
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
			if got := effectiveMinInstances(c.app, time.Now()); got != c.want {
				t.Errorf("effectiveMinInstances(%+v) = %d, want %d", c.app, got, c.want)
			}
			if got := c.app.EffectiveMinInstances(); got != c.want {
				t.Errorf("(*App).EffectiveMinInstances() = %d, want %d", got, c.want)
			}
		})
	}
}
