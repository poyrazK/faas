package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/smtp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// WithRelayHealth returns a separate bridge with explicit health reporting.
// The fixed numeric loopback target cannot be supplied by a message or report.
func (b *PostfixBridge) WithRelayHealth(enabled func() bool, address string) (*PostfixBridge, error) {
	if enabled == nil || !enabled() {
		return nil, ErrDisabled
	}
	if !LoopbackSMTP(address) {
		return nil, ErrInvalid
	}
	copy := *b
	copy.healthEnabled, copy.healthSMTP, copy.probe = enabled, address, probeRelaySMTP
	return &copy, nil
}

func (b *PostfixBridge) relayHealthEnabled() bool {
	return b.enabled() && b.healthEnabled != nil && b.healthEnabled()
}

func (b *PostfixBridge) collectRelayHealth(ctx context.Context) (RelayQueues, error, error) {
	if !b.relayHealthEnabled() {
		return RelayQueues{}, ErrDisabled, ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailRelayCollectionTimeout)
	defer cancel()
	started := time.Now().UTC().Truncate(time.Microsecond)
	queues, inventoryErr := b.queues(ctx)
	if !b.relayHealthEnabled() {
		return queues, inventoryErr, ErrDisabled
	}
	probeErr := b.probe(ctx, b.healthSMTP)
	o := RelayObservation{ID: uuid.NewString(), ObservedAt: started, InventoryOK: inventoryErr == nil, SMTPUp: probeErr == nil}
	if o.InventoryOK {
		o.QueueMessages = min(queues.Total.Count, api.InternalMailRelayMaxObservedMessages)
		o.OldestSeconds = int64(math.Min(math.Ceil(queues.Total.OldestSeconds), api.InternalMailRelayMaxObservedAge.Seconds()))
	}
	return queues, inventoryErr, b.publishRelayHealth(ctx, o)
}

func (b *PostfixBridge) publishRelayHealth(ctx context.Context, observation RelayObservation) error {
	if !b.relayHealthEnabled() {
		return ErrDisabled
	}
	observation, err := observation.normalized()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maild"+RelayHealthPath, bytes.NewReader(raw))
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(req)
	if err != nil {
		return errors.New("internal mail: relay health publication unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, api.InternalMailMaxRelayHealthBytes))
	if response.StatusCode != http.StatusOK {
		return errors.New("internal mail: relay health publication rejected")
	}
	return nil
}

// probeRelaySMTP completes only greeting, EHLO/HELO and QUIT. It cannot submit
// an envelope or body, and both address validation and deadlines precede dialing.
func probeRelaySMTP(ctx context.Context, address string) error {
	if !LoopbackSMTP(address) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailRelayProbeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("internal mail: relay probe unavailable")
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return errors.New("internal mail: relay probe unavailable")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	host, _, _ := net.SplitHostPort(address)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("internal mail: relay probe unavailable")
	}
	defer func() { _ = client.Close() }()
	if err := client.Hello("mail.gregale.internal"); err != nil {
		return errors.New("internal mail: relay probe unavailable")
	}
	if err := client.Quit(); err != nil {
		return errors.New("internal mail: relay probe unavailable")
	}
	return nil
}
