package api

// Outbound webhook DTOs (issue #476 / ADR-076).
//
// Plaintext webhook_secret only appears in CreateAppWebhookRequest
// and UpdateAppWebhookRequest; the response shape (AppWebhookResponse)
// carries a masked constant (AppWebhookSecretMasked) — same posture
// as pkg/api/alerts.go.
//
// Naming mirrors the alert resource shape so the CLI (cmd/gregale)
// and the dashboard can use the same JSON tags verbatim. Update uses
// pointer-everything optionals for the partial-update pattern.
//
// Closed-set vocabularies (retry_policy / event) are typed as plain
// strings here so the DTO file can stay in pkg/api without dragging
// the pkg/state dependency into the cycle. The handler in cmd/apid
// validates each field against the corresponding state.* closed set
// and rejects drift with 400 ErrAppWebhookInvalid.

import "time"

// AppWebhookSecretMaxBytes bounds the plaintext webhook_secret the
// customer may submit on create / update. 256 mirrors the alert-rule
// cap (AlertRuleWebhookSecretMaxBytes) — generous for pasted values
// from secret managers, rejects megabyte uploads.
const AppWebhookSecretMaxBytes = 256

// AppWebhookSecretMasked is the literal returned in every response
// shape that carries the webhook_secret field. Never echo the
// plaintext back to the customer — same posture as
// pkg/api/alerts.go.
const AppWebhookSecretMasked = "***"

// AllowedAppWebhookRetryPolicies is the closed set for the
// `retry_policy` field. Must match state.AppWebhookRetryPolicy's
// enumerated values byte-for-byte; the handler validates membership
// before persisting.
var AllowedAppWebhookRetryPolicies = []string{"default", "aggressive", "none"}

// AllowedAppWebhookEvents is the closed set for the events a webhook
// can subscribe to. It mirrors the SQL CHECK and the CLI vocabulary.
//
// Each entry is the event name persisted in the delivery ledger, so the
// dispatcher can route without a second lookup. Producers may add the row
// directly or use pkg/webhook.Emit after their source mutation commits.
var AllowedAppWebhookEvents = []string{
	"cron.fired", "cron.fired.manually",
	"app.created", "app.deleted", "app.deployed", "app.scaled", "app.parked", "app.woken",
	"build.succeeded", "build.failed",
	"deployment.failed", "rollout.aborted", "error.new", "job.finished", "preview.created", "budget.threshold",
}

// AppWebhookEventFilterLenMax bounds the number of distinct events a
// single webhook can subscribe to. 32 covers the full closed-set today
// and leaves headroom for future expansion.
const AppWebhookEventFilterLenMax = 32

// CreateAppWebhookRequest is the POST /v1/apps/{slug}/webhooks body.
// AppID is the URL slug, not the body — same shape as the per-app
// alert rule routes.
//
// EventFilter is the optional allowlist: empty/nil subscribes to
// every event in AllowedAppWebhookEvents. When non-empty, every
// entry must be a member of the closed set — the handler rejects
// drift with 400 ErrAppWebhookInvalid.
type CreateAppWebhookRequest struct {
	TargetURL     string   `json:"target_url"`
	WebhookSecret string   `json:"webhook_secret"`
	EventFilter   []string `json:"event_filter,omitempty"`
	RetryPolicy   string   `json:"retry_policy,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

// UpdateAppWebhookRequest is the PATCH body. Every editable field
// is pointer-typed so the handler can distinguish "omitted" (leave
// alone) from "zero" (clear). Mirrors UpdateAlertRuleRequest and
// state.UpdateAppWebhookParams.
type UpdateAppWebhookRequest struct {
	TargetURL     *string   `json:"target_url,omitempty"`
	WebhookSecret *string   `json:"webhook_secret,omitempty"`
	EventFilter   *[]string `json:"event_filter,omitempty"`
	RetryPolicy   *string   `json:"retry_policy,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
}

// RotateAppWebhookSecretRequest is the rotate-secret body. Reserved
// for a future "customer supplies plaintext" variant; the rotate
// endpoint always server-mints via crypto/rand so the body is empty
// today.
type RotateAppWebhookSecretRequest struct{}

// AppWebhookResponse is the GET / list / create / update shape.
// Mirrors state.AppWebhook but drops the sealed ciphertext and
// renders the masked constant in webhook_secret_sealed_masked.
// Times are RFC3339 strings (precedent: AlertRuleResponse,
// CronResponse).
type AppWebhookResponse struct {
	ID                        string   `json:"id"`
	AppID                     string   `json:"app_id"`
	AccountID                 string   `json:"account_id"`
	TargetURL                 string   `json:"target_url"`
	WebhookSecretSealedMasked string   `json:"webhook_secret_sealed_masked"`
	EventFilter               []string `json:"event_filter"`
	RetryPolicy               string   `json:"retry_policy"`
	Enabled                   bool     `json:"enabled"`
	CreatedAt                 string   `json:"created_at"`
	UpdatedAt                 string   `json:"updated_at"`
}

// AppWebhookRow is the closed-set-typed counterpart of
// AppWebhookResponse, mirroring state.AppWebhook verbatim. Used by
// the handler at the pkg/api ↔ pkg/state boundary so the conversion
// from typed to string stays in one place. NOT exported on the wire.
type AppWebhookRow struct {
	ID          string
	AppID       string
	AccountID   string
	TargetURL   string
	EventFilter []string
	RetryPolicy string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AppWebhookResponseFromRow maps a wire-shaped row (closed sets as
// strings) to the response DTO. Drops the sealed secret; renders the
// masked constant. Times are RFC3339 strings; zero times serialise
// as empty so the omitempty tag drops them.
//
// Load-bearing shape at the pkg/api ↔ pkg/state boundary: pkg/api/
// webhooks.go defines the DTO + converter; pkg/state defines the
// typed row. Mirrors pkg/api/alerts.go::AlertRuleResponseFromRow.
func AppWebhookResponseFromRow(r AppWebhookRow) AppWebhookResponse {
	return AppWebhookResponse{
		ID:                        r.ID,
		AppID:                     r.AppID,
		AccountID:                 r.AccountID,
		TargetURL:                 r.TargetURL,
		WebhookSecretSealedMasked: AppWebhookSecretMasked,
		EventFilter:               r.EventFilter,
		RetryPolicy:               r.RetryPolicy,
		Enabled:                   r.Enabled,
		CreatedAt:                 FormatAlertTime(r.CreatedAt),
		UpdatedAt:                 FormatAlertTime(r.UpdatedAt),
	}
}

// AppWebhookDeliveryStatus is the closed-set vocabulary for the
// `status` field on AppWebhookDeliveryResponse. Mirrors the DB CHECK
// constraint on app_webhook_deliveries.status.
var AppWebhookDeliveryStatus = []string{
	"pending", "in_flight", "succeeded", "failed", "dead",
}

// AppWebhookDeliveryResponse is the GET /deliveries entry shape.
// Mirrors state.AppWebhookDelivery but renders the status as a plain
// string + RFC3339 timestamps.
//
// DeliveredAt and NextAttemptAt use pointer-everything optionals:
// pending rows have no DeliveredAt (the field is NULL until the
// delivery lands); the field is also NULL for failed rows that have
// not been rescheduled (retry_policy='none' on first failure).
type AppWebhookDeliveryResponse struct {
	ID               string `json:"id"`
	WebhookID        string `json:"webhook_id"`
	AppID            string `json:"app_id"`
	AccountID        string `json:"account_id"`
	Event            string `json:"event"`
	Payload          []byte `json:"payload,omitempty"`
	Attempt          int    `json:"attempt"`
	Status           string `json:"status"`
	LastError        string `json:"last_error,omitempty"`
	LastResponseCode int    `json:"last_response_code,omitempty"`
	NextAttemptAt    string `json:"next_attempt_at"`
	DeliveredAt      string `json:"delivered_at,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// AppWebhookDeliveryRow is the closed-set-typed counterpart of
// AppWebhookDeliveryResponse. Used by the handler at the pkg/api ↔
// pkg/state boundary.
type AppWebhookDeliveryRow struct {
	ID               string
	WebhookID        string
	AppID            string
	AccountID        string
	Event            string
	Payload          []byte
	Attempt          int
	Status           string
	LastError        string
	LastResponseCode int
	NextAttemptAt    time.Time
	DeliveredAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AppWebhookDeliveryResponseFromRow maps a wire-shaped row to the
// response DTO. Payload is dropped when empty so the omitempty tag
// hides the field on retries-after-delivery (the customer has
// already seen the body).
func AppWebhookDeliveryResponseFromRow(r AppWebhookDeliveryRow) AppWebhookDeliveryResponse {
	resp := AppWebhookDeliveryResponse{
		ID:               r.ID,
		WebhookID:        r.WebhookID,
		AppID:            r.AppID,
		AccountID:        r.AccountID,
		Event:            r.Event,
		Payload:          r.Payload,
		Attempt:          r.Attempt,
		Status:           r.Status,
		LastError:        r.LastError,
		LastResponseCode: r.LastResponseCode,
		NextAttemptAt:    FormatAlertTime(r.NextAttemptAt),
		CreatedAt:        FormatAlertTime(r.CreatedAt),
		UpdatedAt:        FormatAlertTime(r.UpdatedAt),
	}
	if r.DeliveredAt != nil {
		resp.DeliveredAt = FormatAlertTime(*r.DeliveredAt)
	}
	return resp
}

// RotateAppWebhookSecretResponse is the body of POST
// /v1/apps/{slug}/webhooks/{id}/rotate-secret. The plaintext is
// server-minted and dropped; only the masked constant + rotation
// timestamp cross the wire.
type RotateAppWebhookSecretResponse struct {
	RotatedAt                 string `json:"rotated_at"`
	WebhookSecretSealedMasked string `json:"webhook_secret_sealed_masked"`
}

// ListAppWebhookDeliveriesOptions are the query knobs for
// Client.ListAppWebhookDeliveries. Status is one of
// pending|in_flight|succeeded|failed|dead or empty for all. PageSize
// caps the response (1..100). PageToken is the opaque cursor from
// the previous page.
type ListAppWebhookDeliveriesOptions struct {
	Status    string
	PageSize  int
	PageToken string
}

// AppWebhookDeliveryListResponse wraps the deliveries slice with a
// cursor-shaped page token (mirrors ListCronRunsResponse shape).
type AppWebhookDeliveryListResponse struct {
	Deliveries []AppWebhookDeliveryResponse `json:"deliveries"`
	NextToken  string                       `json:"next_token,omitempty"`
}

// AppWebhookRetryDeliveryResponse is the POST /deliveries/{id}/retry
// response shape. Returns the freshly-reset row so the customer can
// confirm the attempt counter went back to 0.
type AppWebhookRetryDeliveryResponse struct {
	Delivery AppWebhookDeliveryResponse `json:"delivery"`
}

// FormatAlertTime is a thin alias for the existing AlertRuleResponse
// time formatter. Lives in pkg/api/alerts.go; we re-import here so
// the webhook DTO file stays self-contained for spec-sync purposes.
// Mirrors the precedent of pkg/api/cron_dto.go re-using helpers from
// pkg/api/dto.go.
