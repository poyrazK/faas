package api

import "time"

// Delivery observations never change or reinterpret the business outcome.
type OperationDeliveryInspection struct {
	OperationID           string         `json:"operation_id"`
	BusinessState         OperationState `json:"business_state"`
	OperationExpiresAt    time.Time      `json:"operation_expires_at"`
	ObservedAt            time.Time      `json:"observed_at"`
	State                 string         `json:"state"`
	DeliveryID            string         `json:"delivery_id,omitempty"`
	WebhookID             string         `json:"webhook_id,omitempty"`
	ReplayGeneration      *int           `json:"replay_generation,omitempty"`
	Attempts              int            `json:"attempts"`
	LastResponseCode      int            `json:"last_response_code"`
	ErrorCode             string         `json:"error_code,omitempty"`
	NextAttemptAt         *time.Time     `json:"next_attempt_at,omitempty"`
	DeliveredAt           *time.Time     `json:"delivered_at,omitempty"`
	ReceiverState         string         `json:"receiver_state,omitempty"`
	ReceiverCooldownUntil *time.Time     `json:"receiver_cooldown_until,omitempty"`
}

type OperationDeliveryAttempt struct {
	ReplayGeneration int        `json:"replay_generation"`
	AttemptNumber    int        `json:"attempt_number"`
	Outcome          string     `json:"outcome"`
	ResponseCode     int        `json:"response_code"`
	ErrorCode        string     `json:"error_code,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       time.Time  `json:"finished_at"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
}

type OperationDeliveryAttemptsResponse struct {
	OperationID string                     `json:"operation_id"`
	DeliveryID  string                     `json:"delivery_id"`
	Attempts    []OperationDeliveryAttempt `json:"attempts"`
	NextCursor  string                     `json:"next_cursor,omitempty"`
}

type OperationDeliveryRetryRequest struct {
	RetryID                  string `json:"retry_id"`
	DeliveryID               string `json:"delivery_id"`
	ExpectedReplayGeneration *int   `json:"expected_replay_generation"`
}

// This is an immutable decision receipt, not the delivery's current status.
type OperationDeliveryRetryResponse struct {
	OperationID              string    `json:"operation_id"`
	RetryID                  string    `json:"retry_id"`
	DeliveryID               string    `json:"delivery_id"`
	ExpectedReplayGeneration int       `json:"expected_replay_generation"`
	ReplayGeneration         int       `json:"replay_generation"`
	State                    string    `json:"state"`
	QueuedAt                 time.Time `json:"queued_at"`
	ExpiresAt                time.Time `json:"expires_at"`
}
