package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// EventFeedClient uses only the protected local socket and one producer token.
// Callers enable it explicitly; construction never reads environment or secrets.
type EventFeedClient struct {
	producer, token string
	enabled         func() bool
	client          *http.Client
}

func NewEventFeedClient(socket, producer, token string, enabled func() bool) (*EventFeedClient, error) {
	if enabled == nil || !enabled() {
		return nil, ErrDisabled
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || !validProducerID(producer) || !validToken(token) {
		return nil, ErrInvalid
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &EventFeedClient{producer: producer, token: token, enabled: enabled, client: &http.Client{Transport: transport, Timeout: api.InternalMailAPITimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *EventFeedClient) request(ctx context.Context, consumer, action string, input, output any) error {
	if c == nil || c.enabled == nil || !c.enabled() {
		return ErrDisabled
	}
	route, err := EventConsumerRoute(c.producer, consumer)
	if err != nil {
		return err
	}
	method := http.MethodGet
	if action != "" && action != "pull" && action != "ack" {
		return ErrInvalid
	}
	if action != "" {
		method = http.MethodPost
		route += "/" + action
	}
	var raw []byte
	if input != nil {
		var err error
		raw, err = json.Marshal(input)
		if err != nil {
			return ErrInvalid
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://maild"+route, bytes.NewReader(raw))
	if err != nil {
		return ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if !c.enabled() {
		return ErrDisabled
	}
	response, err := c.client.Do(r)
	if err != nil {
		return errors.New("internal mail event feed unavailable; retry the pending batch")
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusTooManyRequests:
		return ErrQuota
	default:
		return errors.New("internal mail event feed request rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.InternalMailMaxOperatorResponseBytes+1))
	if err != nil || len(body) > api.InternalMailMaxOperatorResponseBytes {
		return ErrInvalid
	}
	if json.Unmarshal(body, output) != nil {
		return ErrInvalid
	}
	return nil
}
func (c *EventFeedClient) Pull(ctx context.Context, consumer string, limit int) (EventFeedBatch, error) {
	if c == nil || c.enabled == nil || !c.enabled() {
		return EventFeedBatch{}, ErrDisabled
	}
	if limit < 1 || limit > api.InternalMailEventFeedMaxPageSize {
		return EventFeedBatch{}, ErrInvalid
	}
	var result EventFeedBatch
	err := c.request(ctx, consumer, "pull", struct {
		Limit int `json:"limit"`
	}{limit}, &result)
	if err == nil {
		err = c.validateBatch(consumer, result)
	}
	return result, err
}
func (c *EventFeedClient) Ack(ctx context.Context, consumer string, input EventFeedAckInput) (EventFeedAck, error) {
	if c == nil || c.enabled == nil || !c.enabled() {
		return EventFeedAck{}, ErrDisabled
	}
	if input.Validate() != nil {
		return EventFeedAck{}, ErrInvalid
	}
	var result EventFeedAck
	err := c.request(ctx, consumer, "ack", input, &result)
	if err == nil && (result.ProducerID != c.producer || result.Consumer != consumer || result.Receipt != input.Receipt || result.Generation != input.Generation || !canonicalOperatorID(result.Cursor)) {
		err = ErrInvalid
	}
	return result, err
}
func (c *EventFeedClient) Status(ctx context.Context, consumer string) (EventConsumerStatus, error) {
	var result EventConsumerStatus
	err := c.request(ctx, consumer, "", nil, &result)
	if err == nil && (result.ProducerID != c.producer || result.Consumer != consumer || result.Generation < 1) {
		err = ErrInvalid
	}
	return result, err
}
func (c *EventFeedClient) validateBatch(consumer string, b EventFeedBatch) error {
	if b.ProducerID != c.producer || b.Consumer != consumer || b.Generation < 1 || len(b.Events) > api.InternalMailEventFeedMaxPageSize || b.Cursor != "" && !canonicalOperatorID(b.Cursor) {
		return ErrInvalid
	}
	if len(b.Events) == 0 {
		if b.Receipt != "" || b.NextCursor != b.Cursor {
			return ErrInvalid
		}
		return nil
	}
	if !canonicalOperatorID(b.Receipt) || b.NextCursor != b.Events[len(b.Events)-1].ID {
		return ErrInvalid
	}
	previous := int64(0)
	seen := map[string]bool{}
	for _, e := range b.Events {
		if !canonicalOperatorID(e.ID) || !canonicalOperatorID(e.MessageID) || !canonicalOperatorID(e.RecipientID) || !canonicalOperatorID(e.DomainID) || e.Sequence <= previous || seen[e.ID] || !validDeliveryEventKind(e.Kind) || e.OccurredAt.IsZero() || e.PublishedAt.IsZero() {
			return ErrInvalid
		}
		previous = e.Sequence
		seen[e.ID] = true
	}
	return nil
}

// ConsumeOnce acknowledges only after every handler call succeeds. The handler
// must deduplicate by event ID alongside its own durable effects: failed batches
// and lost acknowledgments can repeat already processed events.
func (c *EventFeedClient) ConsumeOnce(ctx context.Context, consumer string, limit int, handle func(context.Context, DeliveryEvent) error) (int, error) {
	if c == nil || c.enabled == nil || !c.enabled() {
		return 0, ErrDisabled
	}
	if handle == nil {
		return 0, ErrInvalid
	}
	batch, err := c.Pull(ctx, consumer, limit)
	if err != nil {
		return 0, err
	}
	for _, event := range batch.Events {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !c.enabled() {
			return 0, ErrDisabled
		}
		if err := handle(ctx, event); err != nil {
			return 0, err
		}
	}
	if len(batch.Events) == 0 {
		return 0, nil
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	ack, err := c.Ack(ctx, consumer, EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation})
	if err == nil && ack.Cursor != batch.NextCursor {
		return 0, ErrInvalid
	}
	if err != nil {
		return 0, err
	}
	return len(batch.Events), nil
}
