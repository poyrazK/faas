package state

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Optional seam: retries commit their receipt and transport reset together.
type OperationDeliveryStore interface {
	OperationDeliverySnapshot(context.Context, string, string) (api.OperationDeliveryInspection, error)
	RetryOperationCompletionDelivery(context.Context, string, string, api.OperationDeliveryRetryRequest) (api.OperationDeliveryRetryResponse, error)
}

func validateOperationDeliveryRetry(req api.OperationDeliveryRetryRequest) error {
	if !utf8.ValidString(req.RetryID) || len(req.RetryID) == 0 || len(req.RetryID) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.RetryID, "\x00\r\n") || req.ExpectedReplayGeneration == nil || *req.ExpectedReplayGeneration < 0 || *req.ExpectedReplayGeneration >= math.MaxInt32 {
		return ErrInvalidArgument
	}
	if id, err := uuid.Parse(req.DeliveryID); err != nil || id == uuid.Nil {
		return ErrInvalidArgument
	}
	return nil
}

func operationDeliveryIDsEqual(a, b string) bool {
	x, e := uuid.Parse(a)
	y, f := uuid.Parse(b)
	return e == nil && f == nil && x != uuid.Nil && x == y
}

func operationDeliveryOwned(op Operation, def OperationDefinition, d AppWebhookDelivery) bool {
	var payload struct {
		Operation struct {
			ID string `json:"id"`
		} `json:"operation"`
	}
	return op.CompletionDelivery.DeliveryID == d.ID && d.AccountID == op.AccountID && d.AppID == op.AppID && d.WebhookID == def.Spec.CompletionWebhookID && d.Event == AppWebhookEventOperationFinished && json.Unmarshal(d.Payload, &payload) == nil && payload.Operation.ID == op.ID
}

// Raw dispatcher errors may include receiver URLs. Only categories cross this API.
func OperationDeliveryErrorCode(code int, message string) string {
	if code >= 400 {
		return "receiver_http_error"
	}
	if message != "" {
		return "transport_or_dispatch_error"
	}
	return ""
}

func operationDeliveryObservation(op Operation, d *AppWebhookDelivery, generation int, now time.Time) api.OperationDeliveryInspection {
	r := api.OperationDeliveryInspection{OperationID: op.ID, BusinessState: op.State, OperationExpiresAt: op.ExpiresAt.Truncate(time.Microsecond), ObservedAt: now, State: op.CompletionDelivery.State, DeliveryID: op.CompletionDelivery.DeliveryID}
	if d == nil {
		if r.DeliveryID != "" {
			r.State = "delivery_expired"
		}
		return r
	}
	r.State = string(d.Status)
	r.WebhookID = d.WebhookID
	r.ReplayGeneration = &generation
	r.Attempts = d.Attempt
	r.LastResponseCode = d.LastResponseCode
	r.ErrorCode = OperationDeliveryErrorCode(d.LastResponseCode, d.LastError)
	r.DeliveredAt = d.DeliveredAt
	if d.Status == AppWebhookDeliveryPending || d.Status == AppWebhookDeliveryFailed {
		v := d.NextAttemptAt
		r.NextAttemptAt = &v
	}
	return r
}

func operationDeliveryReceiptMatches(r api.OperationDeliveryRetryResponse, req api.OperationDeliveryRetryRequest) bool {
	return r.RetryID == req.RetryID && operationDeliveryIDsEqual(r.DeliveryID, req.DeliveryID) && r.ExpectedReplayGeneration == *req.ExpectedReplayGeneration
}

func newOperationDeliveryReceipt(op Operation, req api.OperationDeliveryRetryRequest, now time.Time) api.OperationDeliveryRetryResponse {
	return api.OperationDeliveryRetryResponse{OperationID: op.ID, RetryID: req.RetryID, DeliveryID: op.CompletionDelivery.DeliveryID, ExpectedReplayGeneration: *req.ExpectedReplayGeneration, ReplayGeneration: *req.ExpectedReplayGeneration + 1, State: "queued", QueuedAt: now.Truncate(time.Microsecond), ExpiresAt: op.ExpiresAt.Truncate(time.Microsecond)}
}
