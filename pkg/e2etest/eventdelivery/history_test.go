package eventdelivery

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGateValidatesNewestFirstReplayHistory(t *testing.T) {
	finished := time.Now().UTC()
	retained := finished.Add(30 * 24 * time.Hour)
	history := []api.InvocationAttemptResponse{
		{ID: 5, InvocationID: "failed-consumer", ReplayGeneration: 1, Attempt: 1, Outcome: "succeeded", FinishedAt: &finished, RetainUntil: retained},
		{ID: 4, InvocationID: "failed-consumer", Attempt: 3, Outcome: "dead_letter", FinishedAt: &finished, RetainUntil: retained},
		{ID: 3, InvocationID: "failed-consumer", Attempt: 2, Outcome: "retry", FinishedAt: &finished, RetainUntil: retained},
		{ID: 2, InvocationID: "failed-consumer", Attempt: 1, Outcome: "retry", FinishedAt: &finished, RetainUntil: retained},
	}
	if err := verifyHistory(history, "failed-consumer", 3); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"reordered", "missing", "wrong_generation", "wrong_outcome", "unfinished", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			rows := slices.Clone(history)
			switch scenario {
			case "reordered":
				slices.Reverse(rows)
			case "missing":
				rows = rows[:3]
			case "wrong_generation":
				rows[0].ReplayGeneration = 0
			case "wrong_outcome":
				rows[1].Outcome = "retry"
			case "unfinished":
				rows[1].FinishedAt = nil
			case "expired":
				rows[1].RetainUntil = finished.Add(-time.Second)
			}
			if verifyHistory(rows, "failed-consumer", 3) == nil {
				t.Fatal("gate accepted invalid retained replay evidence")
			}
		})
	}
}
