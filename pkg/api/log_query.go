package api

// LogSource identifies the subsystem that produced a customer-visible log
// query event. The first query surface supports the existing runtime stream
// and the durable HTTP request telemetry store.
type LogSource string

const (
	LogSourceRuntime LogSource = "runtime"
	LogSourceHTTP    LogSource = "http"
)

// LogQueryEvent is the stable, source-neutral shape emitted by database-backed
// log queries. Fields that do not apply to a source are omitted so future
// build, deploy, network, and DNS sources can join the same stream without
// changing the existing HTTP event contract.
type LogQueryEvent struct {
	ID           string    `json:"id"`
	Timestamp    string    `json:"timestamp"`
	Source       LogSource `json:"source"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	InstanceID   string    `json:"instance_id,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	Route        string    `json:"route,omitempty"`
	Method       string    `json:"method,omitempty"`
	Status       int       `json:"status,omitempty"`
	Level        string    `json:"level,omitempty"`
	Stream       string    `json:"stream,omitempty"`
	Message      string    `json:"message"`
	LatencyMS    int       `json:"latency_ms,omitempty"`
	Count        int       `json:"count,omitempty"`
	ColdBoot     bool      `json:"cold_boot,omitempty"`
}
