// adr: 650 — summarize existing bounded request logs without exporting application data.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var dataAPIDiagnosticUUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var dataAPIDiagnosticRoutes = []string{"rest", "rpc", "health", "schema", "openapi", "unknown"}

type dataAPIRequestRecord struct {
	Time          string                         `json:"time"`
	RequestID     string                         `json:"request_id"`
	Method        string                         `json:"method"`
	Route         string                         `json:"route"`
	Status        *int                           `json:"status"`
	DurationMS    float64                        `json:"duration_ms"`
	Outcome       string                         `json:"outcome"`
	Code          *string                        `json:"code"`
	Operation     string                         `json:"operation,omitempty"`
	OperationKind string                         `json:"operation_kind,omitempty"`
	ShapeID       string                         `json:"query_shape_id,omitempty"`
	Shape         *dataAPIQueryShape             `json:"query_shape,omitempty"`
	Fingerprint   string                         `json:"schema_fingerprint,omitempty"`
	Admission     *dataAPIAdmissionRecord        `json:"admission,omitempty"`
	Deadline      *dataAPIDeadlineRecord         `json:"deadline,omitempty"`
	Timings       map[string]dataAPITimingRecord `json:"timings,omitempty"`
	ResponseLimit *dataAPIResponseLimitRecord    `json:"response_limit,omitempty"`
	RateLimit     *dataAPIRateLimitRecord        `json:"rate_limit,omitempty"`
}
type dataAPILatency struct {
	P50 *float64 `json:"p50"`
	P95 *float64 `json:"p95"`
	P99 *float64 `json:"p99"`
}
type dataAPIRequestStats struct {
	Requests     int                `json:"requests"`
	Errors       int                `json:"errors"`
	HTTPErrors   int                `json:"http_errors"`
	ServerErrors int                `json:"server_errors"`
	Aborted      int                `json:"aborted"`
	ErrorRate    *float64           `json:"error_rate_percent"`
	LatencyMS    dataAPILatency     `json:"latency_ms"`
	Timings      dataAPITimingStats `json:"timings"`
	durations    []float64
}
type dataAPIFailureCounts struct {
	Authentication     int `json:"authentication"`
	Timeouts           int `json:"timeouts"`
	Unavailable        int `json:"unavailable"`
	ContractMismatch   int `json:"contract_mismatch"`
	Upstream           int `json:"upstream"`
	Overloaded         int `json:"overloaded"`
	RefreshPaused      int `json:"refresh_paused"`
	RefreshUnavailable int `json:"refresh_unavailable"`
	ResponseTooLarge   int `json:"response_too_large"`
	RateLimited        int `json:"rate_limited"`
}
type dataAPIDiagnosticsReport struct {
	Version              int                             `json:"version"`
	Source               string                          `json:"source"`
	Coverage             string                          `json:"coverage"`
	App                  string                          `json:"app,omitempty"`
	Deployment           string                          `json:"deployment,omitempty"`
	Since                string                          `json:"since,omitempty"`
	First                string                          `json:"first_request_time,omitempty"`
	Last                 string                          `json:"last_request_time,omitempty"`
	Partial              bool                            `json:"partial"`
	PartialReasons       []string                        `json:"partial_reasons"`
	InputRecords         int                             `json:"input_records"`
	Ignored              int                             `json:"ignored"`
	Invalid              int                             `json:"invalid"`
	Duplicates           int                             `json:"duplicates"`
	Filtered             int                             `json:"filtered"`
	Gaps                 int                             `json:"gaps"`
	Summary              dataAPIRequestStats             `json:"summary"`
	DataRequests         dataAPIRequestStats             `json:"data_requests"`
	Authentication       dataAPIRequestStats             `json:"authentication"`
	ByRoute              map[string]*dataAPIRequestStats `json:"by_route"`
	Failures             dataAPIFailureCounts            `json:"failures"`
	Admission            dataAPIAdmissionStats           `json:"admission"`
	Deadlines            dataAPIDeadlineStats            `json:"deadlines"`
	RequestID            string                          `json:"request_id,omitempty"`
	Operations           []dataAPIOperationStats         `json:"operations"`
	QueryShapes          []dataAPIShapeStats             `json:"query_shapes"`
	QueryShapeSort       string                          `json:"query_shape_sort"`
	ShapedRequests       int                             `json:"shaped_requests"`
	UnshapedDataRequests int                             `json:"unshaped_data_requests"`
	Matches              []dataAPIRequestRecord          `json:"matches,omitempty"`
}

type dataAPIDiagnosticsCollector struct {
	report      dataAPIDiagnosticsReport
	seen        map[string]bool
	since       time.Time
	first, last time.Time
	shapes      map[string]*dataAPIShapeStats
	operations  map[string]*dataAPIOperationStats
}

func cmdDataAPIDiagnostics(args []string) int {
	fs := newFlagSet("data-api diagnostics", flag.ContinueOnError)
	file := fs.String("file", "", "saved runtime NDJSON or JSON log envelopes for an offline analysis")
	since := fs.String("since", "", "positive lookback duration or RFC3339 timestamp")
	deployment := fs.String("deployment", "", "restrict the hosted retained log sample to a deployment")
	requestID := fs.String("request-id", "", "look up one gateway-generated request UUID")
	shapeSort := fs.String("sort", "requests", "query shape ranking: requests, p95 or errors")
	timeout := fs.Duration("timeout", 2*time.Minute, "entire hosted read deadline (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	targetOK := (*file == "" && fs.NArg() == 1 && api.ValidAppSlug(fs.Arg(0))) || (*file != "" && fs.NArg() == 0 && *deployment == "")
	if !targetOK || *timeout <= 0 || *timeout > time.Hour || (*requestID != "" && !dataAPIDiagnosticUUID.MatchString(*requestID)) || !dataAPIDiagnosticAllowed(*shapeSort, "requests", "p95", "errors") {
		PrintUsage(os.Stderr, "usage: gregale data-api diagnostics [NAME | --file FILE] [--since DURATION|RFC3339] [--deployment ID] [--request-id UUID] [--sort requests|p95|errors] [--timeout DURATION]", "data-api")
		return 1
	}
	normalized, err := normalizeLogsSince(*since, logsSourceRuntime, time.Now())
	if err != nil {
		return printErr("Invalid diagnostics time window", err)
	}
	collector := newDataAPIDiagnosticsCollector(normalized, strings.ToLower(*requestID))
	collector.report.App, collector.report.Deployment = fs.Arg(0), *deployment
	collector.report.QueryShapeSort = *shapeSort
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	var input io.ReadCloser
	if *file != "" {
		collector.report.Source, collector.report.Coverage = "file", "provided_file"
		var info os.FileInfo
		info, err = os.Lstat(*file)
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("offline logs must be a regular NDJSON file")
		}
		if err == nil {
			input, err = openCustomerFile(*file)
		}
	} else {
		collector.report.Source, collector.report.Coverage = "hosted", "retained_sample"
		var client *api.Client
		client, err = authedClient()
		if err == nil {
			input, err = client.StreamAppLogs(ctx, fs.Arg(0), *deployment, false, api.LogFilter{Since: normalized})
		}
	}
	if err != nil {
		return printErr("Could not read Data API logs", err)
	}
	defer func() { _ = input.Close() }()
	stopRead := context.AfterFunc(ctx, func() { _ = input.Close() })
	defer stopRead()
	readDataAPIDiagnostics(ctx, input, *file == "", collector)
	readErr := ctx.Err()
	collector.interrupted(readErr)
	collector.finish()
	if err := writeDataAPIDiagnostics(collector.report); err != nil {
		return printErr("Could not write diagnostics", err)
	}
	if readErr != nil {
		return printErr("Diagnostics interrupted", readErr)
	}
	if collector.report.Partial {
		return 1
	}
	return 0
}

func newDataAPIDiagnosticsCollector(since, requestID string) *dataAPIDiagnosticsCollector {
	c := &dataAPIDiagnosticsCollector{seen: map[string]bool{}, report: dataAPIDiagnosticsReport{Version: 1, Since: since, RequestID: requestID, PartialReasons: []string{}, ByRoute: map[string]*dataAPIRequestStats{}}}
	for _, route := range dataAPIDiagnosticRoutes {
		c.report.ByRoute[route] = &dataAPIRequestStats{}
	}
	c.report.Deadlines.ByPhase = map[string]int{}
	c.report.QueryShapeSort = "requests"
	if since != "" {
		c.since, _ = time.Parse(time.RFC3339Nano, since)
	}
	return c
}
func (c *dataAPIDiagnosticsCollector) partial(reason string) {
	c.report.Partial = true
	for _, value := range c.report.PartialReasons {
		if value == reason {
			return
		}
	}
	c.report.PartialReasons = append(c.report.PartialReasons, reason)
}

func (c *dataAPIDiagnosticsCollector) interrupted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		c.partial("cancelled")
	} else {
		c.partial("timeout")
	}
	return true
}

func parseDataAPIRequestRecord(content string) (dataAPIRequestRecord, bool, error) {
	var record dataAPIRequestRecord
	var envelope struct {
		Event  string  `json:"event"`
		Line   *string `json:"line"`
		Reason string  `json:"reason"`
	}
	if json.Unmarshal([]byte(content), &envelope) != nil {
		if strings.Contains(content, "data_api_request") {
			return record, true, errors.New("invalid request record")
		}
		return record, false, nil
	}
	if envelope.Event != "data_api_request" && envelope.Line != nil {
		content = *envelope.Line
		envelope.Event = ""
		if json.Unmarshal([]byte(content), &envelope) != nil {
			return record, strings.Contains(content, "data_api_request"), errors.New("invalid request record")
		}
	}
	if envelope.Event != "data_api_request" {
		return record, false, nil
	}
	// Unknown fields are discarded. Only the runtime's fixed, safe vocabulary is
	// ever copied into the report, even if a saved file contains arbitrary secrets.
	var fields struct {
		dataAPIRequestRecord
		Duration   *float64        `json:"duration_ms"`
		StatusJSON json.RawMessage `json:"status"`
	}
	if json.Unmarshal([]byte(content), &fields) != nil || fields.Duration == nil || len(fields.StatusJSON) == 0 {
		return record, true, errors.New("incomplete request record")
	}
	record = fields.dataAPIRequestRecord
	record.DurationMS = *fields.Duration
	if string(fields.StatusJSON) != "null" {
		if json.Unmarshal(fields.StatusJSON, &record.Status) != nil {
			return record, true, errors.New("invalid status")
		}
	}
	timestamp, err := time.Parse(time.RFC3339Nano, record.Time)
	if err != nil || !dataAPIDiagnosticUUID.MatchString(record.RequestID) || !dataAPIDiagnosticAllowed(record.Route, dataAPIDiagnosticRoutes...) || !dataAPIDiagnosticAllowed(record.Method, "GET", "HEAD", "POST", "PATCH", "DELETE", "OPTIONS", "PUT", "OTHER") || !dataAPIDiagnosticAllowed(record.Outcome, "completed", "rejected", "authentication_failed", "query_timeout", "unavailable", "upstream_error", "aborted", "overloaded") || math.IsNaN(record.DurationMS) || math.IsInf(record.DurationMS, 0) || record.DurationMS < 0 {
		return record, true, errors.New("invalid request metadata")
	}
	if (record.Status == nil && record.Outcome != "aborted") || (record.Status != nil && (*record.Status < 100 || *record.Status > 599)) {
		return record, true, errors.New("invalid request status")
	}
	if record.Code != nil && !dataAPIDiagnosticAllowed(*record.Code, "origin_not_allowed", "not_found", "invalid_path", "method_not_allowed", "token_required", "token_invalid", "read_only_identity", "serving_contract_unavailable", "data_api_unavailable", "data_api_refresh_paused", "data_api_refresh_unavailable", "request_too_large", "response_too_large", "query_timeout", "request_deadline_exceeded", "schema_document_too_large", "schema_contract_mismatch", "upstream_error", "data_api_overloaded", "data_api_rate_limited") {
		return record, true, errors.New("invalid request code")
	}
	if err := validateDataAPIQueryShape(record); err != nil {
		return record, true, err
	}
	if err := validateDataAPIAdmission(record.Admission); err != nil {
		return record, true, err
	}
	if err := validateDataAPIDeadline(record); err != nil {
		return record, true, err
	}
	if err := validateDataAPITimings(record.Timings); err != nil {
		return record, true, err
	}
	if err := validateDataAPIResponseLimit(record); err != nil {
		return record, true, err
	}
	if err := validateDataAPIRateLimit(record); err != nil {
		return record, true, err
	}
	record.Time = timestamp.UTC().Format(time.RFC3339Nano)
	record.RequestID = strings.ToLower(record.RequestID)
	return record, true, nil
}
func dataAPIDiagnosticAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (c *dataAPIDiagnosticsCollector) observe(content string) bool {
	if c.report.InputRecords >= api.DataAPIDiagnosticsMaxRecords {
		c.partial("record_limit")
		return false
	}
	c.report.InputRecords++
	record, candidate, err := parseDataAPIRequestRecord(content)
	if !candidate {
		// gregale --json logs preserves gap payloads as standalone JSON, even
		// though their SSE event name is no longer present in a saved file.
		var gap struct {
			Reason string  `json:"reason"`
			Line   *string `json:"line"`
			Event  string  `json:"event"`
		}
		if json.Unmarshal([]byte(content), &gap) == nil && gap.Line == nil && (gap.Event == "gap" || dataAPIDiagnosticAllowed(gap.Reason, "seq_below_retained", "since_below_retained")) {
			c.report.Gaps++
			c.partial("log_gap")
			return true
		}
		c.report.Ignored++
		return true
	}
	if err != nil {
		c.report.Invalid++
		c.partial("invalid_records")
		return true
	}
	timestamp, _ := time.Parse(time.RFC3339Nano, record.Time)
	if timestamp.Before(c.since) || (c.report.RequestID != "" && record.RequestID != c.report.RequestID) {
		c.report.Filtered++
		return true
	}
	if c.seen[record.RequestID] {
		c.report.Duplicates++
		return true
	}
	c.seen[record.RequestID] = true
	if c.first.IsZero() || timestamp.Before(c.first) {
		c.first = timestamp
	}
	if c.last.IsZero() || timestamp.After(c.last) {
		c.last = timestamp
	}
	c.report.Summary.add(record)
	c.report.Admission.add(record.Admission)
	c.report.Deadlines.add(record.Deadline)
	c.report.ByRoute[record.Route].add(record)
	if record.Route == "rest" || record.Route == "rpc" {
		c.report.DataRequests.add(record)
		if record.Shape != nil {
			if c.shapes == nil {
				c.shapes = map[string]*dataAPIShapeStats{}
			}
			key := record.Fingerprint + ":" + record.ShapeID + ":" + record.Method + ":" + record.Operation
			stats := c.shapes[key]
			if stats == nil {
				stats = &dataAPIShapeStats{Operation: record.Operation, Kind: record.OperationKind, Method: record.Method, ShapeID: record.ShapeID, Fingerprint: record.Fingerprint, Shape: record.Shape, RequestIDs: []string{}}
				c.shapes[key] = stats
			}
			operationKey := record.Fingerprint + ":" + record.OperationKind + ":" + record.Operation
			if c.operations == nil {
				c.operations = map[string]*dataAPIOperationStats{}
			}
			operation := c.operations[operationKey]
			if operation == nil {
				operation = &dataAPIOperationStats{Operation: record.Operation, Kind: record.OperationKind, Fingerprint: record.Fingerprint}
				c.operations[operationKey] = operation
			}
			operation.Stats.add(record)
			stats.Stats.add(record)
			if len(stats.RequestIDs) < 3 {
				stats.RequestIDs = append(stats.RequestIDs, record.RequestID)
			}
			c.report.ShapedRequests++
		} else {
			c.report.UnshapedDataRequests++
		}
	}
	if record.Outcome == "authentication_failed" {
		c.report.Authentication.add(record)
		c.report.Failures.Authentication++
	}
	if record.Code != nil {
		switch *record.Code {
		case "query_timeout", "request_deadline_exceeded":
			c.report.Failures.Timeouts++
		case "data_api_refresh_paused":
			c.report.Failures.RefreshPaused++
			c.report.Failures.Unavailable++
		case "data_api_refresh_unavailable":
			c.report.Failures.RefreshUnavailable++
			c.report.Failures.Unavailable++
		case "data_api_unavailable", "serving_contract_unavailable":
			c.report.Failures.Unavailable++
		case "schema_contract_mismatch":
			c.report.Failures.ContractMismatch++
		case "upstream_error":
			c.report.Failures.Upstream++
		case "data_api_overloaded":
			c.report.Failures.Overloaded++
		case "response_too_large":
			c.report.Failures.ResponseTooLarge++
		case "data_api_rate_limited":
			c.report.Failures.RateLimited++
		}
	}
	if c.report.RequestID != "" {
		c.report.Matches = append(c.report.Matches, record)
	}
	return true
}
func (s *dataAPIRequestStats) add(record dataAPIRequestRecord) {
	s.Requests++
	s.durations = append(s.durations, record.DurationMS)
	s.Timings.add(record.Timings)
	httpError := record.Status != nil && *record.Status >= 400
	if httpError {
		s.HTTPErrors++
	}
	if record.Status != nil && *record.Status >= 500 {
		s.ServerErrors++
	}
	if record.Outcome == "aborted" {
		s.Aborted++
	}
	if httpError || record.Outcome == "aborted" {
		s.Errors++
	}
}
func (s *dataAPIRequestStats) finish() {
	s.Timings.finish()
	if s.Requests == 0 {
		return
	}
	rate := float64(s.Errors) * 100 / float64(s.Requests)
	s.ErrorRate = &rate
	s.LatencyMS = dataAPIPercentiles(s.durations)
	s.durations = nil
}
func (c *dataAPIDiagnosticsCollector) finish() {
	c.report.QueryShapes = []dataAPIShapeStats{}
	for _, stats := range c.shapes {
		stats.Stats.finish()
		operation := c.operations[stats.Fingerprint+":"+stats.Kind+":"+stats.Operation]
		if operation != nil {
			operation.ShapeCount++
		}
		c.report.QueryShapes = append(c.report.QueryShapes, *stats)
	}
	rankDataAPIQueryShapes(c.report.QueryShapes, c.report.QueryShapeSort)
	c.report.Operations = []dataAPIOperationStats{}
	for _, operation := range c.operations {
		operation.Stats.finish()
		c.report.Operations = append(c.report.Operations, *operation)
	}
	sort.Slice(c.report.Operations, func(i, j int) bool {
		a, b := c.report.Operations[i], c.report.Operations[j]
		if a.Stats.Requests != b.Stats.Requests {
			return a.Stats.Requests > b.Stats.Requests
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Fingerprint < b.Fingerprint
	})
	c.report.Summary.finish()
	c.report.DataRequests.finish()
	c.report.Authentication.finish()
	c.report.Admission.finish()
	for _, stats := range c.report.ByRoute {
		stats.finish()
	}
	if !c.first.IsZero() {
		c.report.First = c.first.UTC().Format(time.RFC3339Nano)
		c.report.Last = c.last.UTC().Format(time.RFC3339Nano)
	}
	sort.Strings(c.report.PartialReasons)
}

// A synchronous bounded SSE reader avoids retaining raw logs or background
// parser goroutines when a terminal frame or sample limit ends the analysis.
func readDataAPIDiagnostics(ctx context.Context, input io.Reader, sse bool, c *dataAPIDiagnosticsCollector) {
	if c.interrupted(ctx.Err()) {
		return
	}
	bounded := &io.LimitedReader{R: input, N: api.DataAPIDiagnosticsMaxBytes + 1}
	scanner := bufio.NewScanner(bounded)
	scanner.Buffer(make([]byte, 4096), api.LogRingBufferBytes)
	event := ""
	var data strings.Builder
	ended := false
	flush := func() bool {
		switch event {
		case "log", "":
			if data.Len() > 0 {
				if !c.observe(data.String()) {
					return false
				}
			}
		case "gap":
			c.report.Gaps++
			c.partial("log_gap")
		case "error", "degraded":
			c.partial("stream_unavailable")
			return false
		case "end":
			var terminal struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal([]byte(data.String()), &terminal) != nil || terminal.Reason != "" {
				c.partial("stream_end_error")
			}
			ended = true
			return false
		default:
			c.partial("unsupported_frame")
		}
		event = ""
		data.Reset()
		return true
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := scanner.Text()
		if !sse {
			if strings.TrimSpace(line) != "" && !c.observe(line) {
				return
			}
			continue
		}
		if line == "" {
			if !flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if data.Len()+len(value)+1 > api.LogRingBufferBytes {
				c.partial("frame_limit")
				return
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if sse && ctx.Err() == nil && scanner.Err() == nil && bounded.N > 0 && (event != "" || data.Len() > 0) {
		if !flush() {
			return
		}
	}
	if c.interrupted(ctx.Err()) {
		return
	}
	if bounded.N == 0 {
		c.partial("byte_limit")
	}
	if scanner.Err() != nil {
		c.partial("read_error")
	}
	if sse && !ended {
		c.partial("missing_end")
	}
}

func writeDataAPIDiagnostics(report dataAPIDiagnosticsReport) error {
	if jsonOutput {
		return writeJSON(report)
	}
	coverage := "Retained log sample"
	if report.Source == "file" {
		coverage = "Provided log file"
	}
	if _, err := fmt.Fprintf(osStdout, "%s; partial: %t\n", coverage, report.Partial); err != nil {
		return err
	}
	for _, group := range []struct {
		name  string
		stats dataAPIRequestStats
	}{{"All routes", report.Summary}, {"REST and RPC", report.DataRequests}, {"Authentication failures", report.Authentication}} {
		if err := writeDataAPIDiagnosticStats(group.name, group.stats); err != nil {
			return err
		}
	}
	for _, route := range dataAPIDiagnosticRoutes {
		if err := writeDataAPIDiagnosticStats(route, *report.ByRoute[route]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(osStdout, "Gateway failures: %d authentication, %d timeouts, %d unavailable, %d contract mismatches, %d upstream, %d overloaded\nIgnored: %d; invalid: %d; duplicates: %d; filtered: %d; gaps: %d\n", report.Failures.Authentication, report.Failures.Timeouts, report.Failures.Unavailable, report.Failures.ContractMismatch, report.Failures.Upstream, report.Failures.Overloaded, report.Ignored, report.Invalid, report.Duplicates, report.Filtered, report.Gaps); err != nil {
		return err
	}
	if report.Failures.RefreshPaused > 0 || report.Failures.RefreshUnavailable > 0 {
		if _, err := fmt.Fprintf(osStdout, "Refresh findings within unavailable: %d paused, %d reader unavailable\n", report.Failures.RefreshPaused, report.Failures.RefreshUnavailable); err != nil {
			return err
		}
	}
	if report.Failures.RateLimited > 0 {
		if _, err := fmt.Fprintf(osStdout, "User rate limits: %d observed (rejected before SQL dispatch)\n", report.Failures.RateLimited); err != nil {
			return err
		}
	}
	if report.Failures.ResponseTooLarge > 0 {
		if _, err := fmt.Fprintf(osStdout, "Response size limits: %d observed (writes may have committed)\n", report.Failures.ResponseTooLarge); err != nil {
			return err
		}
	}
	if err := writeDataAPIAdmission(report.Admission); err != nil {
		return err
	}
	if err := writeDataAPIDeadlines(report.Deadlines); err != nil {
		return err
	}
	if report.First != "" {
		if _, err := fmt.Fprintf(osStdout, "Observed request timestamps: %s to %s\n", report.First, report.Last); err != nil {
			return err
		}
	}
	if report.Partial {
		if _, err := fmt.Fprintf(osStdout, "Partial reasons: %s\n", strings.Join(report.PartialReasons, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(osStdout, "Query-shape coverage: %d shaped, %d unshaped data requests\n", report.ShapedRequests, report.UnshapedDataRequests); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(osStdout, "Query shape ranking: %s (gateway request observations)\n", report.QueryShapeSort); err != nil {
		return err
	}
	for _, operation := range report.Operations {
		if err := writeDataAPIDiagnosticStats(operation.Operation+" ("+operation.Kind+")", operation.Stats); err != nil {
			return err
		}
	}
	for _, shape := range report.QueryShapes {
		if _, err := fmt.Fprintf(osStdout, "%s %s shape=%s schema=%s requests=%d errors=%d\n", shape.Method, shape.Operation, shape.ShapeID, shape.Fingerprint, shape.Stats.Requests, shape.Stats.Errors); err != nil {
			return err
		}
		description, _ := json.Marshal(shape.Shape)
		if _, err := fmt.Fprintf(osStdout, "  Shape: %s; request IDs: %v\n", description, shape.RequestIDs); err != nil {
			return err
		}
		if err := writeDataAPIDiagnosticStats("  Query shape", shape.Stats); err != nil {
			return err
		}
	}

	if report.RequestID != "" {
		if len(report.Matches) == 0 {
			_, err := fmt.Fprintln(osStdout, "No matching request in the available sample.")
			return err
		}
		for _, record := range report.Matches {
			status := "not sent"
			if record.Status != nil {
				status = fmt.Sprint(*record.Status)
			}
			code := "none"
			if record.Code != nil {
				code = *record.Code
			}
			if _, err := fmt.Fprintf(osStdout, "%s %s %s %s HTTP %s %.3f ms %s code=%s\n", record.Time, record.RequestID, record.Method, record.Route, status, record.DurationMS, record.Outcome, code); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeDataAPIDiagnosticStats(name string, stats dataAPIRequestStats) error {
	rate, latency := "n/a", "n/a"
	if stats.ErrorRate != nil {
		rate = fmt.Sprintf("%.2f%%", *stats.ErrorRate)
	}
	if stats.LatencyMS.P50 != nil {
		latency = fmt.Sprintf("%.3f / %.3f / %.3f ms", *stats.LatencyMS.P50, *stats.LatencyMS.P95, *stats.LatencyMS.P99)
	}
	if _, err := fmt.Fprintf(osStdout, "%s: %d requests, %d errors (%s), p50/p95/p99 %s\n", name, stats.Requests, stats.Errors, rate, latency); err != nil {
		return err
	}
	return writeDataAPITimings(stats.Timings)
}
