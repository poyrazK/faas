package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Test sends apid cannot open itself go to meterd (ADR 20261022111812,
// section 3). Once the alerting seal domain is in domain mode, a channel
// destination or a rule's webhook secret opens only with the alerting key,
// which apid does not hold: apid records the request, meterd sends it, and
// apid answers with meterd's result as if it had sent it. While apid still
// opens a value it sends it itself, so a meterd one release older never
// sees a request.

// Waits for meterd's result: a channel send is bounded at 30 seconds, a
// webhook test retries like a real delivery for up to about 80.
var (
	channelTestRelayWait = 45 * time.Second
	webhookTestRelayWait = 2 * time.Minute
	testRelayPoll        = 200 * time.Millisecond
)

// relayTestSend records a test send for meterd and waits for its result.
// The request is deleted either way; a late result is discarded.
func (s *server) relayTestSend(ctx context.Context, accountID, kind, subjectID string, event json.RawMessage, wait time.Duration) (state.AlertTestSendResult, *api.Problem) {
	store, ok := s.store.(state.AlertTestSendStore)
	if !ok {
		return state.AlertTestSendResult{}, api.ErrCapacity("test sends are unavailable on this deployment")
	}
	id := uuid.NewString()
	if _, err := store.CreateAlertTestSend(ctx, state.AlertTestSend{ID: id, AccountID: accountID, Kind: kind, SubjectID: subjectID, Event: event}); err != nil {
		s.log.Warn("record alert test send", "kind", kind, "err", err)
		return state.AlertTestSendResult{}, api.ErrCapacity("could not queue the test send")
	}
	defer func() {
		if err := store.DeleteAlertTestSend(context.WithoutCancel(ctx), accountID, id); err != nil {
			s.log.Warn("delete alert test send", "id", id, "err", err)
		}
	}()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(testRelayPoll)
	defer tick.Stop()
	for {
		row, err := store.GetAlertTestSend(ctx, accountID, id)
		if err == nil && row.State == state.AlertTestSendDone && row.Result != nil {
			return *row.Result, nil
		}
		select {
		case <-ctx.Done():
			return state.AlertTestSendResult{}, api.ErrCapacity("the test send was cancelled")
		case <-deadline.C:
			return state.AlertTestSendResult{}, api.NewProblem(http.StatusGatewayTimeout, api.CodeCapacity, "Test send timed out",
				"the alert sender did not report a result in time; check the destination's recent deliveries before retrying")
		case <-tick.C:
		}
	}
}
