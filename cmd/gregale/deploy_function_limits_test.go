package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestFunctionLimitsLine(t *testing.T) {
	tests := []struct {
		name string
		app  api.AppResponse
		want string
	}{
		{
			name: "defaults",
			app: api.AppResponse{Type: "function", RAMMB: 256, EffectiveLimits: api.AppEffectiveLimits{
				RequestBudgetMS: 30000, IdleTimeoutS: 30, Pause: api.PauseBetweenRequests,
			}},
			want: "256 MB · 30 s timeout · pauses between requests · parks after 30 s idle",
		},
		{
			name: "configured timeout, warm, never paused",
			app: api.AppResponse{Type: "function", RAMMB: 512, RequestTimeoutS: 120, MinInstances: 1, EffectiveLimits: api.AppEffectiveLimits{
				RequestBudgetMS: 30000, IdleTimeoutS: 300, Pause: api.PauseNever,
			}},
			want: "512 MB · 2 min timeout · never paused · keeps 1 warm",
		},
		{
			name: "older API without idle or pause",
			app:  api.AppResponse{Type: "function", RAMMB: 256, EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 45000}},
			want: "256 MB · 45 s timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := main.FunctionLimitsLine(tt.app); got != tt.want {
				t.Fatalf("got %q\nwant %q", got, tt.want)
			}
		})
	}
}
