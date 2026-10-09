package canary

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

type profileRecoveryStore struct {
	*stubStore
	decision api.ProfileCanaryGateDecision
}

func (s *profileRecoveryStore) ProfileCanaryGate(context.Context, CanaryRow) (api.ProfileCanaryGateDecision, error) {
	return s.decision, nil
}

type profileRecoveryClient struct {
	*stubAPID
	calls int
	fail  bool
}

func (c *profileRecoveryClient) AbortProfileRegressedCanary(context.Context, string, int) (api.CanaryAdvanceResponse, error) {
	c.calls++
	if c.fail {
		return api.CanaryAdvanceResponse{}, errors.New("policy context changed")
	}
	return api.CanaryAdvanceResponse{ProfileGate: &api.ProfileCanaryGateDecision{Status: "rolled_back"}}, nil
}

func TestProfileGateRecoveryBeforeStageDwell(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "context_changed"}[fail], func(t *testing.T) {
			now := time.Now()
			row := CanaryRow{ID: "candidate", AppID: "app", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, CanaryStepStarted: now, RolloutState: "rolling_out"}
			store := &profileRecoveryStore{stubStore: &stubStore{rows: []CanaryRow{row}}, decision: api.ProfileCanaryGateDecision{Status: "regressed", AutoRollback: true}}
			client := &profileRecoveryClient{stubAPID: &stubAPID{}, fail: fail}
			p := NewProgression(store, client, nil, nil)
			p.Now = func() time.Time { return now }
			stats, err := p.Once(t.Context())
			if err != nil || client.calls != 1 || len(client.advances) != 0 || stats.Advanced != 0 {
				t.Fatal(stats, client, err)
			}
			if !fail && stats.Aborted != 1 {
				t.Fatal(stats)
			}
			if fail && stats.Errors != 1 {
				t.Fatal(stats)
			}
		})
	}
}
