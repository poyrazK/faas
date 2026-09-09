// Package logdrain delivers customer runtime log records to provider-neutral
// HTTP and OTLP/HTTP endpoints.
//
// The sender deliberately owns only delivery mechanics. Configuration and
// secret lifecycle stay in apid/state, while gatewayd supplies records from
// the existing per-instance log stream. A bounded channel makes a slow
// customer endpoint observable without allowing it to stall request serving.
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
	Kind           Kind
	TargetURL      string
	AuthHeader     string
	QueueSize      int
	RequestTimeout time.Duration
	MaxAttempts    int
	HTTPClient     *http.Client
	OnDropped      func(Record)
	OnDelivered    func(Record)
	OnFailed       func(Record, error)
}

// Sender is an asynchronous, bounded log delivery queue.
type Sender struct {
	cfg    Config
	client *http.Client
	queue  chan Record
	done   chan struct{}
	header http.Header
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
	return &Sender{
		cfg:    cfg,
		client: cfg.HTTPClient,
		queue:  make(chan Record, cfg.QueueSize),
		done:   make(chan struct{}),
		header: header,
	}, nil
}

// Enqueue adds a record without blocking. False means the bounded queue was
// full; the caller's drop metric hook is invoked in that case.
func (s *Sender) Enqueue(record Record) bool {
	select {
	case <-s.done:
		if s.cfg.OnDropped != nil {
			s.cfg.OnDropped(record)
		}
		return false
	default:
	}
	select {
	case s.queue <- record:
		return true
	default:
		if s.cfg.OnDropped != nil {
			s.cfg.OnDropped(record)
		}
		return false
	}
}

// Run drains queued records until ctx is cancelled. It closes the sender so
// later Enqueue calls fail fast.
func (s *Sender) Run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case record := <-s.queue:
			s.deliver(ctx, record)
		}
	}
}

func (s *Sender) deliver(ctx context.Context, record Record) {
	var lastErr error
	for attempt := 0; attempt < s.cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 100 * time.Millisecond
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
			if s.cfg.OnDelivered != nil {
				s.cfg.OnDelivered(record)
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
		return
	}
	if s.cfg.OnFailed != nil && lastErr != nil {
		s.cfg.OnFailed(record, lastErr)
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
