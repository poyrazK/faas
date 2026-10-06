package safedeploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recordingAlertRollback struct {
	recordingAPID
	receipt api.AlertRollback
	err     error
	fire    string
	calls   int
}

func (r *recordingAlertRollback) ProcessAlertRollback(_ context.Context, fire string) (api.AlertRollback, error) {
	r.calls++
	r.fire = fire
	return r.receipt, r.err
}
func TestActionDispatcherUsesDurableFireOnly(t *testing.T) {
	rule := sampleRule()
	for _, scenario := range []string{"complete", "blocked", "failed", "wrong fire", "wrong rule", "transport"} {
		t.Run(scenario, func(t *testing.T) {
			client := &recordingAlertRollback{receipt: api.AlertRollback{ID: "fire-1", RuleID: rule.ID, AppID: rule.AppID, AccountID: rule.AccountID, Status: "complete"}}
			switch scenario {
			case "blocked", "failed":
				client.receipt.Status = scenario
			case "wrong fire":
				client.receipt.ID = "other"
			case "wrong rule":
				client.receipt.RuleID = "other"
			case "transport":
				client.err = errors.New("503")
			}
			dispatcher := NewActionDispatcher(client, discardLog(), "meterd:safedeploy") // no resolver: no new selection is possible
			err := dispatcher.ExecuteClaimed(context.Background(), rule, "fire-1", 42, time.Now())
			if (err == nil) != (scenario == "complete" || scenario == "blocked") {
				t.Fatalf("unexpected result %v", err)
			}
			if client.calls != 1 || client.fire != "fire-1" || client.rollbackCalls != 0 || client.recoverCalls != 0 {
				t.Fatalf("legacy or retargeted call %+v", client)
			}
		})
	}
}
