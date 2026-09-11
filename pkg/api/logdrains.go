package api

import "time"

// AppLogDrainAuthHeaderMaxBytes bounds the single sealed authentication
// header accepted for a customer log destination.
const AppLogDrainAuthHeaderMaxBytes = 4096

const AppLogDrainAuthHeaderMasked = "***"

const (
	AppLogDrainHealthStatusUnknown  = "unknown"
	AppLogDrainHealthStatusHealthy  = "healthy"
	AppLogDrainHealthStatusDegraded = "degraded"
	AppLogDrainHealthStatusInactive = "inactive"
)

// NormalizeAppLogDrainHealthStatus keeps persistence corruption or a future
// status extension from becoming an arbitrary dashboard class or wire value.
func NormalizeAppLogDrainHealthStatus(status string) string {
	switch status {
	case AppLogDrainHealthStatusHealthy, AppLogDrainHealthStatusDegraded, AppLogDrainHealthStatusInactive:
		return status
	default:
		return AppLogDrainHealthStatusUnknown
	}
}

// SanitizeAppLogDrainHealthError returns only the short summaries the runtime
// is allowed to expose to customers. Raw endpoint errors can contain response
// bodies, URLs, or credential-adjacent data and must never cross this boundary.
func SanitizeAppLogDrainHealthError(summary string) string {
	switch summary {
	case "", "delivery queue dropped records", "source log gap observed",
		"endpoint returned an unsuccessful HTTP status", "endpoint request failed",
		"endpoint request could not be built", "delivery failed":
		return summary
	default:
		return "delivery failed"
	}
}

var AllowedAppLogDrainKinds = []string{"http_json", "otlp"}

type CreateAppLogDrainRequest struct {
	Kind       string `json:"kind"`
	TargetURL  string `json:"target_url"`
	AuthHeader string `json:"auth_header,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type UpdateAppLogDrainRequest struct {
	Kind       *string `json:"kind,omitempty"`
	TargetURL  *string `json:"target_url,omitempty"`
	AuthHeader *string `json:"auth_header,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

type AppLogDrainResponse struct {
	ID               string `json:"id"`
	AppID            string `json:"app_id"`
	AccountID        string `json:"account_id"`
	Kind             string `json:"kind"`
	TargetURL        string `json:"target_url"`
	AuthHeaderMasked string `json:"auth_header_masked"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type AppLogDrainRow struct {
	ID            string
	AppID         string
	AccountID     string
	Kind          string
	TargetURL     string
	HasAuthHeader bool
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AppLogDrainHealthResponse is the customer-safe delivery snapshot for one
// configured drain. It contains no endpoint credentials or raw transport
// errors; empty timestamps mean that the event has not happened yet.
type AppLogDrainHealthResponse struct {
	LogDrainID            string `json:"log_drain_id"`
	Status                string `json:"status"`
	Active                bool   `json:"active"`
	QueueDepth            int    `json:"queue_depth"`
	QueueCapacity         int    `json:"queue_capacity"`
	PendingRecords        int    `json:"pending_records"`
	PendingBytes          int64  `json:"pending_bytes"`
	PendingBytesCapacity  int64  `json:"pending_bytes_capacity"`
	DeadLetterTotal       int64  `json:"dead_letter_total"`
	OldestPendingAt       string `json:"oldest_pending_at,omitempty"`
	DeliveredTotal        int64  `json:"delivered_total"`
	FailedTotal           int64  `json:"failed_total"`
	DroppedTotal          int64  `json:"dropped_total"`
	RetriesTotal          int64  `json:"retries_total"`
	StreamReconnectsTotal int64  `json:"stream_reconnects_total"`
	GapsTotal             int64  `json:"gaps_total"`
	LastSuccessAt         string `json:"last_success_at,omitempty"`
	LastFailureAt         string `json:"last_failure_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	UpdatedAt             string `json:"updated_at"`
}

// AppLogDrainAnalyticsResponse is the bounded customer-facing delivery
// history for one runtime log destination. Rates are derived from terminal
// outcomes (delivered, failed, or dropped); an empty interval has a zero rate.
type AppLogDrainAnalyticsResponse struct {
	LogDrainID     string                       `json:"log_drain_id"`
	Window         string                       `json:"window"`
	BucketInterval string                       `json:"bucket_interval"`
	From           string                       `json:"from"`
	To             string                       `json:"to"`
	Buckets        []AppLogDrainAnalyticsBucket `json:"buckets"`
	Summary        AppLogDrainAnalyticsSummary  `json:"summary"`
}

type AppLogDrainAnalyticsBucket struct {
	Start            string  `json:"start"`
	Delivered        int64   `json:"delivered"`
	Failed           int64   `json:"failed"`
	Dropped          int64   `json:"dropped"`
	Retries          int64   `json:"retries"`
	DeadLetters      int64   `json:"dead_letters"`
	PendingRecords   int     `json:"pending_records"`
	PendingBytes     int64   `json:"pending_bytes"`
	SuccessRate      float64 `json:"success_rate"`
	AverageLatencyMS float64 `json:"average_latency_ms"`
}

type AppLogDrainAnalyticsSummary struct {
	Delivered        int64   `json:"delivered"`
	Failed           int64   `json:"failed"`
	Dropped          int64   `json:"dropped"`
	Retries          int64   `json:"retries"`
	DeadLetters      int64   `json:"dead_letters"`
	SuccessRate      float64 `json:"success_rate"`
	AverageLatencyMS float64 `json:"average_latency_ms"`
}

func AppLogDrainResponseFromRow(r AppLogDrainRow) AppLogDrainResponse {
	masked := ""
	if r.HasAuthHeader {
		masked = AppLogDrainAuthHeaderMasked
	}
	return AppLogDrainResponse{
		ID:               r.ID,
		AppID:            r.AppID,
		AccountID:        r.AccountID,
		Kind:             r.Kind,
		TargetURL:        r.TargetURL,
		AuthHeaderMasked: masked,
		Enabled:          r.Enabled,
		CreatedAt:        FormatAlertTime(r.CreatedAt),
		UpdatedAt:        FormatAlertTime(r.UpdatedAt),
	}
}
