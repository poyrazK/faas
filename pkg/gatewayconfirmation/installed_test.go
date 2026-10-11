package gatewayconfirmation

// adr: 693

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type installedRecorder struct {
	app, session, candidate string
	calls                   int
	err                     error
}

func (r *installedRecorder) RecordRuntimeUpgradeGateway(_ context.Context, app, session, candidate string) error {
	r.app, r.session, r.candidate = app, session, candidate
	r.calls++
	return r.err
}

func TestInstalledOnlyAcknowledgesExactSingleWeightedCandidate(t *testing.T) {
	tests := []struct {
		name, session string
		rows          []gateway.DeploymentWeightsRow
		want          int
	}{
		{"disabled", "", []gateway.DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 100}}, 0},
		{"empty", "process", nil, 0},
		{"dark", "process", []gateway.DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 0}}, 0},
		{"split", "process", []gateway.DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 50}, {ID: "old", TrafficPercent: 50}}, 0},
		{"invalid_total", "process", []gateway.DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 100}, {ID: "other", TrafficPercent: 100}}, 0},
		{"unnormalized", "process", []gateway.DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 90}}, 0},
		{"candidate", "process", []gateway.DeploymentWeightsRow{{ID: "old", TrafficPercent: 0}, {ID: "candidate", TrafficPercent: 100}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sentinel := errors.New("synthetic publication failure")
			r := &installedRecorder{err: sentinel}
			err := RecordInstalled(t.Context(), r, tt.session, "app", tt.rows)
			if r.calls != tt.want || (tt.want == 1 && (!errors.Is(err, sentinel) || r.candidate != "candidate" || r.app != "app" || r.session != tt.session)) || (tt.want == 0 && err != nil) {
				t.Fatal(r, err)
			}
		})
	}
}
