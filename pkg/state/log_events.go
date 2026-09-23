package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// LogEventSource is the closed producer vocabulary admitted to the durable
// customer log ledger (ADR-213). Adding a source requires an ADR amendment and
// a producer redaction review.
type LogEventSource string

const (
	LogEventSourceRuntime LogEventSource = "runtime"
	LogEventSourceBuild   LogEventSource = "build"
	LogEventSourceDeploy  LogEventSource = "deploy"
	LogEventSourceHTTP    LogEventSource = "http"
	LogEventSourceNetwork LogEventSource = "network"
	LogEventSourceDNS     LogEventSource = "dns"
)

const (
	MaxLogEventPage         = 200
	maxLogEventMessageRunes = 16_384
	maxLogEventFieldsBytes  = 32 * 1024
)

// LogEvent is one bounded, redacted projection from a source system into the
// customer-queryable ledger. Empty optional fields are stored as SQL NULL.
// Fields must be a JSON object and must never contain request bodies, headers,
// credentials, environment values, or unrestricted span attributes.
type LogEvent struct {
	ID            string
	OccurredAt    time.Time
	AccountID     string
	AppID         string
	DeploymentID  string
	InstanceID    string
	Source        LogEventSource
	SourceEventID string
	RequestID     string
	TraceID       string
	Route         string
	Method        string
	Status        int
	Level         string
	Stream        string
	Message       string
	LatencyMS     *int
	Occurrences   int
	ColdBoot      bool
	Fields        json.RawMessage
}

// LogEventFilter is the stable keyset query contract. Since is inclusive,
// Until is exclusive, and BeforeAt+BeforeID are the last tuple returned by the
// previous page. AccountID and AppID are both required for IDOR-safe reads.
type LogEventFilter struct {
	AccountID    string
	AppID        string
	Since        time.Time
	Until        time.Time
	BeforeAt     time.Time
	BeforeID     string
	Source       LogEventSource
	DeploymentID string
	RequestID    string
	Route        string
	Status       int
	Limit        int
}

// LogEventStore is the ADR-213 durable projection boundary. apid is the only
// production writer; source daemons publish through authenticated internal
// transports rather than receiving database credentials.
type LogEventStore interface {
	InsertLogEvent(ctx context.Context, event LogEvent) (LogEvent, error)
	ListLogEvents(ctx context.Context, filter LogEventFilter) (events []LogEvent, hasMore bool, err error)
}

func validLogEventSource(source LogEventSource) bool {
	switch source {
	case LogEventSourceRuntime, LogEventSourceBuild, LogEventSourceDeploy,
		LogEventSourceHTTP, LogEventSourceNetwork, LogEventSourceDNS:
		return true
	default:
		return false
	}
}

func normalizeLogEventForInsert(event LogEvent, now time.Time) (LogEvent, error) {
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.AppID = strings.TrimSpace(event.AppID)
	event.DeploymentID = strings.TrimSpace(event.DeploymentID)
	event.InstanceID = strings.TrimSpace(event.InstanceID)
	event.SourceEventID = strings.TrimSpace(event.SourceEventID)
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.TraceID = strings.TrimSpace(event.TraceID)
	event.Route = strings.TrimSpace(event.Route)
	event.Method = strings.TrimSpace(event.Method)
	event.Level = strings.ToLower(strings.TrimSpace(event.Level))
	event.Stream = strings.ToLower(strings.TrimSpace(event.Stream))

	if _, err := uuid.Parse(event.AccountID); err != nil {
		return LogEvent{}, errors.New("state: log event requires a valid account id")
	}
	if _, err := uuid.Parse(event.AppID); err != nil {
		return LogEvent{}, errors.New("state: log event requires a valid app id")
	}
	if event.DeploymentID != "" {
		if _, err := uuid.Parse(event.DeploymentID); err != nil {
			return LogEvent{}, errors.New("state: log event deployment id must be a UUID")
		}
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	} else if _, err := uuid.Parse(event.ID); err != nil {
		return LogEvent{}, errors.New("state: log event id must be a UUID")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now.UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	if !validLogEventSource(event.Source) {
		return LogEvent{}, fmt.Errorf("state: invalid log event source %q", event.Source)
	}
	for name, bound := range map[string]struct {
		value string
		limit int
	}{
		"instance_id":     {event.InstanceID, 256},
		"source_event_id": {event.SourceEventID, 256},
		"request_id":      {event.RequestID, 256},
		"trace_id":        {event.TraceID, 128},
		"route":           {event.Route, 256},
		"method":          {event.Method, 32},
	} {
		if utf8.RuneCountInString(bound.value) > bound.limit {
			return LogEvent{}, fmt.Errorf("state: log event %s exceeds %d characters", name, bound.limit)
		}
	}
	if event.Status != 0 && (event.Status < 100 || event.Status > 599) {
		return LogEvent{}, errors.New("state: log event status must be between 100 and 599")
	}
	if event.Level != "" && event.Level != "trace" && event.Level != "debug" &&
		event.Level != "info" && event.Level != "warn" && event.Level != "error" && event.Level != "fatal" {
		return LogEvent{}, fmt.Errorf("state: invalid log event level %q", event.Level)
	}
	if event.Stream != "" && event.Stream != "stdout" && event.Stream != "stderr" &&
		event.Stream != "system" && event.Stream != "access" {
		return LogEvent{}, fmt.Errorf("state: invalid log event stream %q", event.Stream)
	}
	messageRunes := utf8.RuneCountInString(event.Message)
	if messageRunes < 1 || messageRunes > maxLogEventMessageRunes {
		return LogEvent{}, fmt.Errorf("state: log event message must contain 1..%d characters", maxLogEventMessageRunes)
	}
	if event.LatencyMS != nil && *event.LatencyMS < 0 {
		return LogEvent{}, errors.New("state: log event latency must not be negative")
	}
	if event.Occurrences == 0 {
		event.Occurrences = 1
	}
	if event.Occurrences < 1 {
		return LogEvent{}, errors.New("state: log event occurrences must be positive")
	}
	if len(event.Fields) == 0 {
		event.Fields = json.RawMessage(`{}`)
	}
	if len(event.Fields) > maxLogEventFieldsBytes {
		return LogEvent{}, fmt.Errorf("state: log event fields exceed %d bytes", maxLogEventFieldsBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event.Fields, &fields); err != nil || fields == nil {
		return LogEvent{}, errors.New("state: log event fields must be a JSON object")
	}
	event.Fields = append(json.RawMessage(nil), event.Fields...)
	return event, nil
}

func normalizeLogEventFilter(filter LogEventFilter) (LogEventFilter, error) {
	filter.AccountID = strings.TrimSpace(filter.AccountID)
	filter.AppID = strings.TrimSpace(filter.AppID)
	filter.DeploymentID = strings.TrimSpace(filter.DeploymentID)
	filter.RequestID = strings.TrimSpace(filter.RequestID)
	filter.Route = strings.TrimSpace(filter.Route)
	filter.BeforeID = strings.TrimSpace(filter.BeforeID)
	if _, err := uuid.Parse(filter.AccountID); err != nil {
		return LogEventFilter{}, errors.New("state: log query requires a valid account id")
	}
	if _, err := uuid.Parse(filter.AppID); err != nil {
		return LogEventFilter{}, errors.New("state: log query requires a valid app id")
	}
	if filter.DeploymentID != "" {
		if _, err := uuid.Parse(filter.DeploymentID); err != nil {
			return LogEventFilter{}, errors.New("state: log query deployment id must be a UUID")
		}
	}
	if filter.Source != "" && !validLogEventSource(filter.Source) {
		return LogEventFilter{}, fmt.Errorf("state: invalid log query source %q", filter.Source)
	}
	if filter.Status != 0 && (filter.Status < 100 || filter.Status > 599) {
		return LogEventFilter{}, errors.New("state: log query status must be between 100 and 599")
	}
	if filter.Since.IsZero() || filter.Until.IsZero() || !filter.Since.Before(filter.Until) {
		return LogEventFilter{}, errors.New("state: log query requires a valid since/until window")
	}
	filter.Since = filter.Since.UTC()
	filter.Until = filter.Until.UTC()
	hasBeforeAt := !filter.BeforeAt.IsZero()
	hasBeforeID := filter.BeforeID != ""
	if hasBeforeAt != hasBeforeID {
		return LogEventFilter{}, errors.New("state: log query cursor requires before time and id together")
	}
	if hasBeforeID {
		if _, err := uuid.Parse(filter.BeforeID); err != nil {
			return LogEventFilter{}, errors.New("state: log query cursor id must be a UUID")
		}
		filter.BeforeAt = filter.BeforeAt.UTC()
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > MaxLogEventPage {
		filter.Limit = MaxLogEventPage
	}
	return filter, nil
}

func cloneLogEvent(event LogEvent) LogEvent {
	event.Fields = append(json.RawMessage(nil), event.Fields...)
	if event.LatencyMS != nil {
		latency := *event.LatencyMS
		event.LatencyMS = &latency
	}
	return event
}
