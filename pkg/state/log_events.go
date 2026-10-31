package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
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
	// TraceID is an exact W3C trace-id filter, separate from RequestID's
	// backwards-compatible request_id-or-trace_id matching behavior.
	TraceID string
	Route   string
	Status  int
	Limit   int
	// Levels, StatusClass and Query are the customer search filters
	// (ADR-20261018083536). Levels matches any listed severity; StatusClass
	// (1..5) matches a status hundred and excludes Status; Query is a
	// case-insensitive literal substring of the message.
	Levels      []string
	StatusClass int
	Query       string
	// Timeout bounds the read with a statement timeout; zero means none.
	// A tripped timeout returns ErrLogSearchTimeout.
	Timeout time.Duration
}

// ErrLogSearchTimeout reports that a bounded log query hit its statement
// timeout; callers ask the customer to narrow the window or filters.
var ErrLogSearchTimeout = errors.New("state: log search timed out")

// ErrLogReadBusy reports a log read refused because the account, or apid
// as a whole, already has its bounded number of log reads in flight.
var ErrLogReadBusy = errors.New("state: too many log reads in flight")

// LogEventFacetCount is one facet value and the summed occurrences of the
// events carrying it.
type LogEventFacetCount struct {
	Value string
	Count int64
}

// LogEventFacets counts a filtered ledger window by source, level, status
// class ("2xx".."5xx") and route. Routes holds at most the requested route
// limit, busiest first; RoutesTruncated reports that more routes matched.
type LogEventFacets struct {
	Total           int64
	Sources         []LogEventFacetCount
	Levels          []LogEventFacetCount
	StatusClasses   []LogEventFacetCount
	Routes          []LogEventFacetCount
	RoutesTruncated bool
}

// logEventLevels is log_events_level_check's closed vocabulary.
var logEventLevels = map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true, "fatal": true}

const (
	logEventQueryMinRunes = 2
	logEventQueryMaxRunes = 256
)

// LogEventStore is the ADR-213 durable projection boundary. apid is the only
// production writer; source daemons publish through authenticated internal
// transports rather than receiving database credentials.
type LogEventStore interface {
	InsertLogEvent(ctx context.Context, event LogEvent) (LogEvent, error)
	ListLogEvents(ctx context.Context, filter LogEventFilter) (events []LogEvent, hasMore bool, err error)
	ListAccountTraceLogEvents(ctx context.Context, filter AccountTraceLogFilter) (events []LogEvent, hasMore bool, err error)
	// FacetLogEvents counts the events ListLogEvents would match (cursor and
	// limit ignored), keeping the routeLimit busiest routes.
	FacetLogEvents(ctx context.Context, filter LogEventFilter, routeLimit int) (LogEventFacets, error)
}

// AccountTraceLogFilter selects the newest events carrying one exact trace id
// across a set of an account's apps in one query, for `gregale trace`.
// AccountID stays in the predicate next to the app set (ADR-213 IDOR guard).
type AccountTraceLogFilter struct {
	AccountID string
	AppIDs    []string
	TraceID   string
	Source    LogEventSource
	Since     time.Time
	Until     time.Time
	Limit     int
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
	filter.TraceID = strings.TrimSpace(filter.TraceID)
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
	if len(filter.TraceID) > 128 {
		return LogEventFilter{}, errors.New("state: log query trace id exceeds 128 characters")
	}
	if filter.Source != "" && !validLogEventSource(filter.Source) {
		return LogEventFilter{}, fmt.Errorf("state: invalid log query source %q", filter.Source)
	}
	if filter.Status != 0 && (filter.Status < 100 || filter.Status > 599) {
		return LogEventFilter{}, errors.New("state: log query status must be between 100 and 599")
	}
	if filter.StatusClass != 0 && (filter.StatusClass < 1 || filter.StatusClass > 5 || filter.Status != 0) {
		return LogEventFilter{}, errors.New("state: log query status class must be 1..5 and excludes an exact status")
	}
	levels := make([]string, 0, len(filter.Levels))
	for _, level := range filter.Levels {
		level = strings.ToLower(strings.TrimSpace(level))
		if !logEventLevels[level] {
			return LogEventFilter{}, fmt.Errorf("state: invalid log query level %q", level)
		}
		levels = append(levels, level)
	}
	filter.Levels = levels
	filter.Query = strings.TrimSpace(filter.Query)
	if n := utf8.RuneCountInString(filter.Query); filter.Query != "" && (n < logEventQueryMinRunes || n > logEventQueryMaxRunes) {
		return LogEventFilter{}, fmt.Errorf("state: log query text must be %d..%d characters", logEventQueryMinRunes, logEventQueryMaxRunes)
	}
	if filter.Timeout < 0 {
		filter.Timeout = 0
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

func normalizeAccountTraceLogFilter(filter AccountTraceLogFilter) (AccountTraceLogFilter, error) {
	filter.AccountID = strings.TrimSpace(filter.AccountID)
	filter.TraceID = strings.TrimSpace(filter.TraceID)
	if _, err := uuid.Parse(filter.AccountID); err != nil {
		return AccountTraceLogFilter{}, errors.New("state: trace log query requires a valid account id")
	}
	appIDs := make([]string, 0, len(filter.AppIDs))
	for _, appID := range filter.AppIDs {
		appID = strings.TrimSpace(appID)
		if _, err := uuid.Parse(appID); err != nil {
			return AccountTraceLogFilter{}, errors.New("state: trace log query app ids must be UUIDs")
		}
		appIDs = append(appIDs, appID)
	}
	filter.AppIDs = appIDs
	if filter.TraceID == "" || len(filter.TraceID) > 128 {
		return AccountTraceLogFilter{}, errors.New("state: trace log query requires a trace id of at most 128 characters")
	}
	if filter.Source != "" && !validLogEventSource(filter.Source) {
		return AccountTraceLogFilter{}, fmt.Errorf("state: invalid trace log query source %q", filter.Source)
	}
	if filter.Since.IsZero() || filter.Until.IsZero() || !filter.Since.Before(filter.Until) {
		return AccountTraceLogFilter{}, errors.New("state: trace log query requires a valid since/until window")
	}
	filter.Since = filter.Since.UTC()
	filter.Until = filter.Until.UTC()
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

func (f *LogEventFacets) add(dim, value string, count int64) {
	c := LogEventFacetCount{Value: value, Count: count}
	switch dim {
	case "total":
		f.Total = count
	case "source":
		f.Sources = append(f.Sources, c)
	case "level":
		f.Levels = append(f.Levels, c)
	case "status":
		f.StatusClasses = append(f.StatusClasses, c)
	case "route":
		f.Routes = append(f.Routes, c)
	}
}

// finish orders status classes by class and every other facet busiest first
// (ties by value), then keeps routeLimit routes.
func (f *LogEventFacets) finish(routeLimit int) {
	for _, list := range [][]LogEventFacetCount{f.Sources, f.Levels, f.Routes} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Count != list[j].Count {
				return list[i].Count > list[j].Count
			}
			return list[i].Value < list[j].Value
		})
	}
	sort.Slice(f.StatusClasses, func(i, j int) bool { return f.StatusClasses[i].Value < f.StatusClasses[j].Value })
	if len(f.Routes) > routeLimit {
		f.Routes = f.Routes[:routeLimit]
		f.RoutesTruncated = true
	}
}

// logEventMatches is the in-memory twin of logEventFilterSQL (cursor aside).
func logEventMatches(event LogEvent, f LogEventFilter) bool {
	switch {
	case event.AccountID != f.AccountID || event.AppID != f.AppID:
		return false
	case event.OccurredAt.Before(f.Since) || !event.OccurredAt.Before(f.Until):
		return false
	case f.Source != "" && event.Source != f.Source:
		return false
	case f.DeploymentID != "" && event.DeploymentID != f.DeploymentID:
		return false
	case f.RequestID != "" && event.RequestID != f.RequestID && event.TraceID != f.RequestID:
		return false
	case f.TraceID != "" && event.TraceID != f.TraceID:
		return false
	case f.Route != "" && event.Route != f.Route:
		return false
	case f.Status != 0 && event.Status != f.Status:
		return false
	case f.StatusClass != 0 && (event.Status < f.StatusClass*100 || event.Status >= f.StatusClass*100+100):
		return false
	case len(f.Levels) > 0 && !slices.Contains(f.Levels, event.Level):
		return false
	case f.Query != "" && !strings.Contains(strings.ToLower(event.Message), strings.ToLower(f.Query)):
		return false
	}
	return true
}
