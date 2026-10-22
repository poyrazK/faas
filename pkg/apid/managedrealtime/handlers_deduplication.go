package managedrealtime

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func writeRealtimeDeduplicationProblem(w http.ResponseWriter, full *state.RealtimeConsumerGroupDeduplicationFullError) {
	seconds := (full.RetryAfterMilliseconds + 999) / 1000
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	problem := api.NewProblem(http.StatusTooManyRequests, "realtime_group_deduplication_full", "Consumer group event ID capacity reached",
		fmt.Sprintf("event IDs: limit %d, observed %d; wait for an existing ID to expire; live IDs are retained and existing snapshots can still be claimed; see https://gregale.dev/docs/realtime", api.RealtimeConsumerGroupDeduplicationKeysMax, api.RealtimeConsumerGroupDeduplicationKeysMax))
	problem.RetryAfterSeconds = &seconds
	api.WriteProblem(w, problem)
}
