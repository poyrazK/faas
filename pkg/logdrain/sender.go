// Package logdrain delivers customer runtime log records to provider-neutral
// HTTP and OTLP/HTTP endpoints.
//
// The sender deliberately owns only delivery mechanics. Configuration and
// secret lifecycle stay in apid/state, while gatewayd supplies records from
// the existing per-instance log stream. Production uses a durable local
// outbox so a slow endpoint is observable without losing records on restart;
// the bounded channel remains available for lightweight callers and tests.
package logdrain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Kind is the wire encoding used by a drain.
type Kind string

const (
	KindHTTPJSON Kind = "http_json"
	KindOTLP     Kind = "otlp"
)

const (
	defaultQueueSize      = 256
	defaultRequestTimeout = 5 * time.Second
	defaultMaxAttempts    = 3
	maxURLBytes           = 2048
	maxAuthHeaderBytes    = 4096
)

// Record is one runtime log line. Timestamps are retained as time.Time so
// both encodings can represent nanosecond precision without reparsing.
type Record struct {
	AppID        string    `json:"app_id"`
	AccountID    string    `json:"account_id"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	InstanceID   string    `json:"instance_id"`
	Sequence     uint64    `json:"sequence"`
	Stream       string    `json:"stream"`
	Line         string    `json:"line"`
	WrittenAt    time.Time `json:"written_at"`
}

// Config controls a Sender. AuthHeader is a single "Name: value" pair; its
// value is never included in errors or logs.
type Config struct {
	Kind       Kind
	TargetURL  string
	AuthHeader string
	// DurableQueue, when set, makes Enqueue synchronous with a local fsync.
	// The sender then acknowledges records only after a successful 2xx
	// response, so a restart resumes at the first unacknowledged record.
	DurableQueue   *Queue
	QueueSize      int
	RequestTimeout time.Duration
	MaxAttempts    int
	HTTPClient     *http.Client
	OnDropped      func(Record)
	OnDelivered    func(Record)
	OnFailed       func(Record, error)
	// OnQueueDepth is called after queue mutations and once at construction.
	// The capacity is fixed for the sender lifetime, while depth is the
	// current number of records waiting for delivery.
	OnQueueDepth func(depth, capacity int)
	// OnRetry is called before each retry attempt. attempt is one-based and
	// is greater than one for retries.
	OnRetry func(record Record, attempt int)
	// OnDeliveredLatency reports end-to-end time from enqueue to a successful
	// response. It includes queue wait and endpoint latency.
	OnDeliveredLatency func(record Record, latency time.Duration)
	// OnDurableQueue reports the persistent outbox after each mutation and
	// once at construction. It is not called for the legacy memory queue.
	OnDurableQueue func(stats QueueStats)
	// OnDeadLetter runs after retry exhaustion once the failed record has been
	// durably copied to the bounded dead-letter file.
	OnDeadLetter func(record Record, err error)
	// OnQueueStorageError reports a durable queue read/write failure. The
	// sender leaves the current record pending and retries on a later wakeup.
	OnQueueStorageError func(error)
}

// Sender is an asynchronous log delivery queue. Production senders use a
// durable outbox; the legacy in-memory queue is retained for compatibility.
type Sender struct {
	cfg      Config
	client   *http.Client
	queue    chan queuedRecord
	durable  *Queue
	wake     chan struct{}
	done     chan struct{}
	header   http.Header
	queueMu  sync.Mutex
	doneOnce sync.Once
}

type queuedRecord struct {
	record     Record
	enqueuedAt time.Time
}

// New validates configuration and creates a sender. Run must be called by
// the owner to consume the queue.
func New(cfg Config) (*Sender, error) {
	if cfg.Kind != KindHTTPJSON && cfg.Kind != KindOTLP {
		return nil, fmt.Errorf("log drain: unsupported kind %q", cfg.Kind)
	}
	u, err := url.Parse(cfg.TargetURL)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("log drain: target_url must be an http(s) URL without userinfo")
	}
	if len(cfg.TargetURL) > maxURLBytes {
		return nil, fmt.Errorf("log drain: target_url exceeds %d bytes", maxURLBytes)
	}
	header, err := parseAuthHeader(cfg.AuthHeader)
	if err != nil {
		return nil, err
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.RequestTimeout}
	}
	sender := &Sender{
		cfg:     cfg,
		client:  cfg.HTTPClient,
		durable: cfg.DurableQueue,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		header:  header,
	}
	if cfg.DurableQueue == nil {
		sender.queue = make(chan queuedRecord, cfg.QueueSize)
		sender.observeQueueDepth()
	} else {
		sender.observeDurableQueue()
	}
	return sender, nil
}

// Enqueue adds a record to the sender. Durable senders synchronously fsync the
// local outbox before returning; false means shutdown, a full outbox, or a
// storage failure, and the caller's drop hook is invoked in that case.
func (s *Sender) Enqueue(record Record) bool {
	if s.durable != nil {
		s.queueMu.Lock()
		select {
		case <-s.done:
			s.queueMu.Unlock()
			s.drop(record)
			return false
		default:
		}
		err := s.durable.Enqueue(record, time.Now().UTC())
		s.queueMu.Unlock()
		if err != nil {
			if !errors.Is(err, ErrQueueFull) {
				s.queueStorageError(err)
			}
			s.drop(record)
			s.observeDurableQueue()
			return false
		}
		s.observeDurableQueue()
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return true
	}

	select {
	case <-s.done:
		s.drop(record)
		return false
	default:
	}

	s.queueMu.Lock()
	// Run can close the sender after the first check above. Re-check while
	// holding the queue lock so a concurrent shutdown cannot enqueue after
	// the queue has been drained.
	select {
	case <-s.done:
		s.queueMu.Unlock()
		s.drop(record)
		return false
	default:
	}
	select {
	case s.queue <- queuedRecord{record: record, enqueuedAt: time.Now()}:
		s.observeQueueDepthLocked()
		s.queueMu.Unlock()
		return true
	default:
		s.queueMu.Unlock()
		s.drop(record)
		return false
	}
}

// LastSequences returns the durable source cursor used by the log-stream
// consumer. The legacy in-memory sender has no restart-safe cursor.
func (s *Sender) LastSequences() map[string]int64 {
	if s.durable == nil {
		return make(map[string]int64)
	}
	return s.durable.LastSequences()
}

// Run drains queued records until ctx is cancelled. It closes the sender so
// later Enqueue calls fail fast.
func (s *Sender) Run(ctx context.Context) {
	defer s.closeDone()
	if s.durable != nil {
		s.runDurable(ctx)
		return
	}
	for {
		select {
		case <-ctx.Done():
			s.dropQueued()
			return
		case item := <-s.queue:
			s.queueMu.Lock()
			s.observeQueueDepthLocked()
			s.queueMu.Unlock()
			s.deliver(ctx, item)
		}
	}
}

func (s *Sender) runDurable(ctx context.Context) {
	for {
		item, ok, err := s.durable.Next()
		if err != nil {
			s.queueStorageError(err)
			if !waitForQueueWake(ctx, s.wake, time.Second) {
				return
			}
			continue
		}
		if !ok {
			if !waitForQueueWake(ctx, s.wake, 0) {
				return
			}
			continue
		}
		s.deliverDurable(ctx, item)
		if ctx.Err() != nil {
			return
		}
	}
}

func waitForQueueWake(ctx context.Context, wake <-chan struct{}, retry time.Duration) bool {
	if retry <= 0 {
		select {
		case <-ctx.Done():
			return false
		case <-wake:
			return true
		}
	}
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *Sender) deliverDurable(ctx context.Context, item QueueItem) {
	record := item.Record
	attempts := item.Attempts
	var lastErr error
	for attempts < s.cfg.MaxAttempts {
		attempts++
		if err := s.durable.MarkAttempt(item, attempts); err != nil {
			s.queueStorageError(err)
			return
		}
		if attempts > 1 {
			if s.cfg.OnRetry != nil {
				s.cfg.OnRetry(record, attempts)
			}
			backoff := time.Duration(attempts-1) * 100 * time.Millisecond
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		status, err := s.post(ctx, record)
		if err == nil && status >= 200 && status < 300 {
			if err := s.durable.Ack(item); err != nil {
				s.queueStorageError(err)
				return
			}
			if s.cfg.OnDelivered != nil {
				s.cfg.OnDelivered(record)
			}
			if s.cfg.OnDeliveredLatency != nil && !item.EnqueuedAt.IsZero() {
				s.cfg.OnDeliveredLatency(record, time.Since(item.EnqueuedAt))
			}
			s.observeDurableQueue()
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("log drain: endpoint returned status %d", status)
		}
		if !retryableStatus(status) && err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
	}
	if lastErr == nil {
		lastErr = errors.New("log drain: delivery failed")
	}
	if err := s.durable.DeadLetter(item, attempts, lastErr); err != nil {
		s.queueStorageError(err)
		return
	}
	if s.cfg.OnFailed != nil {
		s.cfg.OnFailed(record, lastErr)
	}
	if s.cfg.OnDeadLetter != nil {
		s.cfg.OnDeadLetter(record, lastErr)
	}
	s.observeDurableQueue()
}

func (s *Sender) deliver(ctx context.Context, item queuedRecord) {
	record := item.record
	var lastErr error
	for attempt := 1; attempt <= s.cfg.MaxAttempts; attempt++ {
		if attempt > 1 {
			if s.cfg.OnRetry != nil {
				s.cfg.OnRetry(record, attempt)
			}
			backoff := time.Duration(attempt-1) * 100 * time.Millisecond
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				s.drop(record)
				return
			case <-timer.C:
			}
		}
		status, err := s.post(ctx, record)
		if err == nil && status >= 200 && status < 300 {
			if s.cfg.OnDelivered != nil {
				s.cfg.OnDelivered(record)
			}
			if s.cfg.OnDeliveredLatency != nil && !item.enqueuedAt.IsZero() {
				s.cfg.OnDeliveredLatency(record, time.Since(item.enqueuedAt))
			}
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("log drain: endpoint returned status %d", status)
		}
		if !retryableStatus(status) && err == nil {
			break
		}
	}
	if ctx.Err() != nil {
		s.drop(record)
		return
	}
	if s.cfg.OnFailed != nil && lastErr != nil {
		s.cfg.OnFailed(record, lastErr)
	}
}

func (s *Sender) closeDone() {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	s.closeDoneLocked()
}

func (s *Sender) closeDoneLocked() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Sender) observeQueueDepth() {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	s.observeQueueDepthLocked()
}

func (s *Sender) observeQueueDepthLocked() {
	if s.cfg.OnQueueDepth != nil {
		s.cfg.OnQueueDepth(len(s.queue), cap(s.queue))
	}
}

func (s *Sender) observeDurableQueue() {
	if s.durable == nil {
		return
	}
	if s.cfg.OnDurableQueue != nil {
		s.cfg.OnDurableQueue(s.durable.Stats())
	}
}

func (s *Sender) queueStorageError(err error) {
	if s.cfg.OnQueueStorageError != nil {
		s.cfg.OnQueueStorageError(err)
	}
}

func (s *Sender) drop(record Record) {
	if s.cfg.OnDropped != nil {
		s.cfg.OnDropped(record)
	}
}

func (s *Sender) dropQueued() {
	s.queueMu.Lock()
	s.closeDoneLocked()
	dropped := make([]Record, 0, len(s.queue))
	for {
		select {
		case item := <-s.queue:
			dropped = append(dropped, item.record)
		default:
			s.observeQueueDepthLocked()
			s.queueMu.Unlock()
			for _, record := range dropped {
				s.drop(record)
			}
			return
		}
	}
}

func (s *Sender) post(ctx context.Context, record Record) (int, error) {
	body, contentType, err := encode(s.cfg.Kind, record)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TargetURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("log drain: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "gregale-log-drain/1")
	for key, values := range s.header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("log drain: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	return resp.StatusCode, nil
}

func encode(kind Kind, record Record) ([]byte, string, error) {
	switch kind {
	case KindHTTPJSON:
		body, err := json.Marshal(record)
		return body, "application/json", err
	case KindOTLP:
		body, err := json.Marshal(makeOTLPPayload(record))
		return body, "application/json", err
	default:
		return nil, "", fmt.Errorf("log drain: unsupported kind %q", kind)
	}
}

type otlpLogsPayload struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpLogRecord struct {
	TimeUnixNano         string         `json:"timeUnixNano"`
	ObservedTimeUnixNano string         `json:"observedTimeUnixNano"`
	SeverityText         string         `json:"severityText,omitempty"`
	Body                 otlpAnyValue   `json:"body"`
	Attributes           []otlpKeyValue `json:"attributes"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue string `json:"stringValue,omitempty"`
}

func makeOTLPPayload(record Record) otlpLogsPayload {
	nanos := record.WrittenAt.UnixNano()
	if record.WrittenAt.IsZero() {
		nanos = time.Now().UnixNano()
	}
	attrs := []otlpKeyValue{
		{Key: "faas.account.id", Value: otlpAnyValue{StringValue: record.AccountID}},
		{Key: "faas.app.id", Value: otlpAnyValue{StringValue: record.AppID}},
		{Key: "faas.instance.id", Value: otlpAnyValue{StringValue: record.InstanceID}},
		{Key: "faas.stream", Value: otlpAnyValue{StringValue: record.Stream}},
		{Key: "faas.sequence", Value: otlpAnyValue{StringValue: fmt.Sprint(record.Sequence)}},
	}
	if record.DeploymentID != "" {
		attrs = append(attrs, otlpKeyValue{Key: "faas.deployment.id", Value: otlpAnyValue{StringValue: record.DeploymentID}})
	}
	return otlpLogsPayload{ResourceLogs: []otlpResourceLogs{{
		Resource: otlpResource{Attributes: []otlpKeyValue{{Key: "service.name", Value: otlpAnyValue{StringValue: "gregale"}}}},
		ScopeLogs: []otlpScopeLogs{{
			Scope: otlpScope{Name: "gregale/log-drain"},
			LogRecords: []otlpLogRecord{{
				TimeUnixNano:         fmt.Sprint(nanos),
				ObservedTimeUnixNano: fmt.Sprint(time.Now().UnixNano()),
				Body:                 otlpAnyValue{StringValue: record.Line},
				Attributes:           attrs,
			}},
		}},
	}}}
}

func parseAuthHeader(raw string) (http.Header, error) {
	if raw == "" {
		return make(http.Header), nil
	}
	if len(raw) > maxAuthHeaderBytes || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errors.New("log drain: auth header is invalid")
	}
	name, value, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" || strings.Contains(value, ":") {
		return nil, errors.New("log drain: auth header must be a single non-empty Name: value pair")
	}
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	if name == "" || name == "Content-Length" || name == "Host" {
		return nil, errors.New("log drain: auth header name is not allowed")
	}
	h := make(http.Header)
	h.Set(name, strings.TrimSpace(value))
	return h, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}
