// adr: 117 — unknown historical traffic intent must not be guessed.
package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryRefusesUnrecoverableIntent(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"retry_intent":null}`, `{"retry_intent":{"version":"v2","traffic_percent":10,"traffic_percent_explicit":true,"service_rollout":false}}`,
		`{"retry_intent":{"version":"v1","traffic_percent":101,"traffic_percent_explicit":true,"service_rollout":false}}`,
		`{"retry_intent":{"version":"v1","traffic_percent":10}}`,
		`{"retry_intent":{"version":"v1","traffic_percent":null,"traffic_percent_explicit":true,"service_rollout":false}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			dep := state.Deployment{Status: state.DeployFailed, Kind: state.DeploymentKindImage, TrafficPercentExplicit: true, StageState: json.RawMessage(raw)}
			if _, err := state.RetryDeploymentInput(dep, time.Now()); !errors.Is(err, state.ErrRetryIntentUnavailable) {
				t.Fatalf("retry guessed intent: %v", err)
			}
		})
	}
}
