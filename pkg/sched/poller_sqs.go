// poller_sqs.go — SQS-compatible long-poll poller
// (issue #757 / ADR-0NN, commit #12 of feat-triggers-mega).
//
// The kind=sqs_compat trigger wraps an external SQS-compatible
// HTTP queue (AWS SQS, LocalStack, faas-queue, etc.) and feeds
// it through the same batch envelope as every other trigger.
//
// Distinct from poller_queue.go:
//
//   - kind=queue       in-platform FIFO/delay through the
//                      unified `invocations` table (rows already
//                      committed before the poller sees them).
//   - kind=sqs_compat  external HTTP queue, SQS-shaped API
//                      (long-poll receive + ack/nack via receipt
//                      handle + visibility timeout).
//
// Wire protocol: we speak the SQS subset —
//
//   ReceiveMessage  POST {QueueURL}/receive?wait=LongPollSecs
//                   → { Messages: [{ MessageId, Body, ReceiptHandle, ... }] }
//   DeleteMessage   POST {QueueURL}/delete  body: { ReceiptHandle }
//   ReleaseMessage  POST {QueueURL}/release body: { ReceiptHandle, VisibilityTimeout }
//
// The "queue URL" is a single base; the four operations hang off
// it. This matches what most SQS-compatible brokers expose
// (faas-queue, LocalStack's SQS shim, ElasticMQ).
//
// Ack: POST .../delete with each receipt handle. The broker
// removes the message from its queue.
//
// Nack: POST .../release with the receipt handle + visibility
// timeout. The broker re-makes the message visible after the
// timeout so it can be re-delivered. poison_record becomes
// delete (drop without re-deliver) — the dispatcher has already
// recorded the dead-letter row.

package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// sqsPoller is one per kind=sqs_compat trigger. Holds an
// http.Client (with timeout) and the per-trigger config.
type sqsPoller struct {
	client    *http.Client
	baseURL   string
	longPollS int
	batchMax  int

	mu       sync.Mutex
	inFlight map[string]string // receiptHandle → receiptHandle (for sanity)
}

// sqsConfig is the per-kind config blob.
//
// Schema (validated in pkg/gregalemanifest.validateKindConfig):
//
//	{
//	  "queue_url":      "http://faas-queue:9090/queues/ext-jobs",
//	  "long_poll_secs": 20
//	}
type sqsConfig struct {
	QueueURL    string `json:"queue_url"`
	LongPollSec int    `json:"long_poll_secs,omitempty"`
}

func decodeSQSConfig(t sqlc.Trigger) (sqsConfig, error) {
	var cfg sqsConfig
	if len(t.Config) == 0 {
		return cfg, fmt.Errorf("sqs_poller: trigger missing config")
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("sqs_poller: decode config: %w", err)
	}
	if cfg.QueueURL == "" {
		return cfg, fmt.Errorf("sqs_poller: trigger missing queue_url")
	}
	u, err := url.Parse(cfg.QueueURL)
	if err != nil {
		return cfg, fmt.Errorf("sqs_poller: invalid queue_url %q: %w", cfg.QueueURL, err)
	}
	// Audit round 2 finding #2 (PR #910): url.Parse only fails on
	// truly malformed strings (missing brackets, invalid escape
	// sequences). A schemeless URL like
	// `faas-queue:9090/queues/ext-jobs` parses successfully with
	// Scheme="" + Host="" + Path=`faas-queue:9090/queues/ext-jobs`,
	// and at Poll time the poller concatenates
	// `s.baseURL + "/receive?wait=20"` to get
	// `faas-queue:9090/queues/ext-jobs/receive?wait=20`, which
	// http.NewRequestWithContext parses with Scheme="faas-queue"
	// and a malformed Host — the dial fails with the generic
	// "no Host in request URL" transport error logged at runtime,
	// NOT at config validation time. Reject schemeless URLs AND
	// URLs that parse but lack a host up front.
	if u.Scheme != "http" && u.Scheme != "https" {
		return cfg, fmt.Errorf("sqs_poller: invalid queue_url %q: must include http:// or https:// scheme", cfg.QueueURL)
	}
	if u.Host == "" {
		return cfg, fmt.Errorf("sqs_poller: invalid queue_url %q: missing host", cfg.QueueURL)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return cfg, fmt.Errorf("sqs_poller: invalid queue_url %q: credentials, query parameters, and fragments are forbidden", cfg.QueueURL)
	}
	if cfg.LongPollSec < 0 || cfg.LongPollSec > 20 {
		// SQS-compatible brokers cap long-poll at 20s (AWS SQS
		// itself). Higher values are silently clamped.
		cfg.LongPollSec = 20
	}
	if cfg.LongPollSec == 0 {
		cfg.LongPollSec = 5
	}
	return cfg, nil
}

// sqsReceiveResponse is the SQS-shaped subset of the receive
// endpoint's response. We accept any extra fields the broker
// returns and ignore them.
type sqsReceiveResponse struct {
	Messages []sqsMessage `json:"Messages"`
}

type sqsMessage struct {
	MessageID     string `json:"MessageId"`
	Body          string `json:"Body"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

// newSQSPoller constructs the poller.
//
// http.Client: tuned for long-poll timeouts (longPollS+5s)
// + idle connection reuse. No KeepAlive tweak — Go's default
// pool size of 100 is more than enough for trigger counts in
// the per-account caps.
func newSQSPoller(t sqlc.Trigger) (triggerSource, error) {
	cfg, err := decodeSQSConfig(t)
	if err != nil {
		return nil, err
	}
	c := &http.Client{
		Timeout: time.Duration(cfg.LongPollSec+5) * time.Second,
		Transport: &http.Transport{
			DialContext:         oci.EgressDialContext(nil),
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	return &sqsPoller{
		client:    c,
		baseURL:   cfg.QueueURL,
		longPollS: cfg.LongPollSec,
		batchMax:  10,
		inFlight:  map[string]string{},
	}, nil
}

// Kind returns the trigger kind this poller handles.
func (s *sqsPoller) Kind() string { return "sqs_compat" }

// Poll long-polls the queue for up to longPollS+5 seconds and
// returns whatever the broker delivered.
//
// Endpoint: POST {base}/receive?wait={longPollS}
// Body:    {"max": batchMax}
// Reply:   sqsReceiveResponse
func (s *sqsPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	limit := s.batchMax
	if t.BatchSizeMax > 0 && t.BatchSizeMax < int32(limit) {
		limit = int(t.BatchSizeMax)
	}
	body, _ := json.Marshal(map[string]int{"max": limit})
	u := fmt.Sprintf("%s/receive?wait=%d", s.baseURL, s.longPollS)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return PollResult{Error: fmt.Errorf("sqs_poller: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return PollResult{Error: fmt.Errorf("sqs_poller: receive: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == 204 {
		// Empty long-poll response.
		return PollResult{Records: []SourceRecord{}}
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.TriggerBrokerErrorBodyMaxBytes))
		return PollResult{Error: fmt.Errorf("sqs_poller: receive status %d: %s", resp.StatusCode, raw)}
	}
	payloadBudget := int64(t.PayloadMaxBytes)
	if payloadBudget < 1 {
		payloadBudget = 1
	}
	responseLimit := api.TriggerBrokerEnvelopeOverheadBytes + payloadBudget*api.TriggerBrokerJSONExpansion
	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return PollResult{Error: fmt.Errorf("sqs_poller: read response: %w", err)}
	}
	if int64(len(raw)) > responseLimit {
		return PollResult{Error: fmt.Errorf("sqs_poller: response exceeds %d-byte safety limit", responseLimit)}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return PollResult{Records: []SourceRecord{}}
	}
	var got sqsReceiveResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return PollResult{Error: fmt.Errorf("sqs_poller: decode: %w", err)}
	}
	if len(got.Messages) > limit {
		return PollResult{Error: fmt.Errorf("sqs_poller: broker returned %d messages, limit is %d", len(got.Messages), limit)}
	}
	out := make([]SourceRecord, 0, len(got.Messages))
	for _, m := range got.Messages {
		if m.ReceiptHandle == "" {
			continue
		}
		out = append(out, SourceRecord{
			ItemIdentifier: m.ReceiptHandle,
			Payload:        []byte(m.Body),
			Headers:        map[string]string{"MessageId": m.MessageID},
			Metadata:       map[string]any{"queue_url": s.baseURL},
			ReceivedAt:     time.Now(),
		})
		s.mu.Lock()
		s.inFlight[m.ReceiptHandle] = m.ReceiptHandle
		s.mu.Unlock()
	}
	if out == nil {
		out = []SourceRecord{}
	}
	return PollResult{Records: out}
}

// Ack deletes each receipt handle from the broker.
//
// Endpoint: POST {base}/delete
// Body:    {"receipt_handles": [...]}
// Reply:   200 OK on success, 4xx on per-handle failure.
//
// Best-effort: a failed delete is logged at the dispatcher
// layer (broker-side duplicate delivery will be deduped by the
// function's idempotency-at-message-id story).
func (s *sqsPoller) Ack(ctx context.Context, _ sqlc.Trigger, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.deleteReceipts(ctx, ids)
}

// deleteReceipts is the shared body of Ack and the
// poison_record branch of Nack.
func (s *sqsPoller) deleteReceipts(ctx context.Context, ids []string) error {
	body, _ := json.Marshal(map[string][]string{"receipt_handles": ids})
	u := s.baseURL + "/delete"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sqs_poller: build delete: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sqs_poller: delete: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sqs_poller: delete status %d: %s", resp.StatusCode, raw)
	}
	s.mu.Lock()
	for _, id := range ids {
		delete(s.inFlight, id)
	}
	s.mu.Unlock()
	return nil
}

// Nack releases the receipt handles back into the queue after a
// visibility timeout so the broker can re-deliver them.
//
// Endpoint: POST {base}/release
// Body:    {"receipt_handles": [...], "visibility_timeout": 30}
//
// poison_record becomes delete (no re-delivery).
func (s *sqsPoller) Nack(ctx context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	if reason == triggerReasonPoisonRecord {
		return s.deleteReceipts(ctx, ids)
	}
	body, _ := json.Marshal(map[string]any{
		"receipt_handles":    ids,
		"visibility_timeout": 30,
	})
	u := s.baseURL + "/release"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sqs_poller: build release: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sqs_poller: release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sqs_poller: release status %d: %s", resp.StatusCode, raw)
	}
	s.mu.Lock()
	for _, id := range ids {
		delete(s.inFlight, id)
	}
	s.mu.Unlock()
	return nil
}

// Close releases the http.Client's idle connection pool.
func (s *sqsPoller) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.inFlight {
		delete(s.inFlight, k)
	}
	s.client.CloseIdleConnections()
	return nil
}

func init() {
	registerPoller("sqs_compat", func(t sqlc.Trigger) (triggerSource, error) {
		return newSQSPoller(t)
	})
}
