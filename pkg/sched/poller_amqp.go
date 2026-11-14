// poller_amqp.go — RabbitMQ / AMQP 0-9-1 consumer poller.
//
// Supports connecting to external RabbitMQ brokers, inspecting queue backlog
// (lag/depth) for autoscaling, pulling message batches with back-pressure,
// and acknowledging or nacking messages.

package sched

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// amqpBrokerOp is the minimal broker interface for AMQP operations.
// Abstracted so unit tests can stub queue inspection and message dispatch.
type amqpBrokerOp interface {
	QueueInspect(context.Context, string) (amqp.Queue, error)
	Get(context.Context, string, bool) (amqp.Delivery, bool, error)
	Ack(context.Context, uint64, bool) error
	Nack(context.Context, uint64, bool, bool) error
	Close() error
}

type realAMQPBrokerOp struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	raw  net.Conn
}

const amqpOperationTimeout = 5 * time.Second

// AMQP's synchronous RPCs have no context API. Closing the transport on
// cancellation unblocks the library reader and every pending channel RPC;
// heartbeats alone cannot detect a broker that answers pings but stalls RPCs.
func amqpCall(ctx context.Context, raw net.Conn, call func() error) error {
	ctx, cancel := context.WithTimeout(ctx, amqpOperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	err := call()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (r *realAMQPBrokerOp) QueueInspect(ctx context.Context, name string) (q amqp.Queue, err error) {
	err = amqpCall(ctx, r.raw, func() error {
		q, err = r.ch.QueueDeclarePassive(name, false, false, false, false, nil)
		return err
	})
	return q, err
}

func (r *realAMQPBrokerOp) Get(ctx context.Context, queue string, autoAck bool) (msg amqp.Delivery, ok bool, err error) {
	err = amqpCall(ctx, r.raw, func() error {
		msg, ok, err = r.ch.Get(queue, autoAck)
		return err
	})
	return msg, ok, err
}

func (r *realAMQPBrokerOp) Ack(ctx context.Context, tag uint64, multiple bool) error {
	return amqpCall(ctx, r.raw, func() error { return r.ch.Ack(tag, multiple) })
}

func (r *realAMQPBrokerOp) Nack(ctx context.Context, tag uint64, multiple bool, requeue bool) error {
	return amqpCall(ctx, r.raw, func() error { return r.ch.Nack(tag, multiple, requeue) })
}

func (r *realAMQPBrokerOp) Close() error {
	if r.conn != nil {
		return r.conn.CloseDeadline(time.Now().Add(amqpOperationTimeout))
	}
	return nil
}

type amqpConfig struct {
	URL      string `json:"url"`
	Queue    string `json:"queue"`
	Consumer string `json:"consumer,omitempty"`
}

func decodeAMQPConfig(t sqlc.Trigger) (amqpConfig, error) {
	var cfg amqpConfig
	if len(t.Config) == 0 {
		return cfg, errors.New("amqp_poller: trigger missing config")
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("amqp_poller: decode config: %w", err)
	}
	if cfg.URL == "" {
		return cfg, errors.New("amqp_poller: trigger missing url")
	}
	if cfg.Queue == "" {
		return cfg, errors.New("amqp_poller: trigger missing queue")
	}
	return cfg, nil
}

type amqpPoller struct {
	op       amqpBrokerOp
	queue    string
	batchMax int

	mu       sync.Mutex
	inFlight map[string]uint64 // itemIdentifier -> deliveryTag
}

func newAMQPPoller(t sqlc.Trigger) (triggerSource, error) {
	cfg, err := decodeAMQPConfig(t)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || (parsed.Scheme != "amqp" && parsed.Scheme != "amqps") || parsed.Host == "" {
		return nil, fmt.Errorf("amqp_poller: invalid broker URL: %q", cfg.URL)
	}

	dialContext := oci.EgressDialContext(&net.Dialer{Timeout: 5 * time.Second})
	var raw net.Conn
	var handshakeTimer *time.Timer
	defer func() {
		if handshakeTimer != nil {
			handshakeTimer.Stop()
		}
	}()
	amqpCfg := amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var err error
			raw, err = dialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			// Custom Dial replaces the library's default handshake deadline.
			// Open clears this deadline after TLS/auth/protocol negotiation.
			if err := raw.SetDeadline(time.Now().Add(amqpOperationTimeout)); err != nil {
				_ = raw.Close()
				return nil, err
			}
			// The AMQP heartbeater may update socket read deadlines during
			// negotiation. Keep a separate wall-clock bound on the handshake.
			transport := raw
			handshakeTimer = time.AfterFunc(amqpOperationTimeout, func() { _ = transport.Close() })
			return raw, nil
		},
	}
	if parsed.Scheme == "amqps" {
		amqpCfg.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	conn, err := amqp.DialConfig(cfg.URL, amqpCfg)
	if err != nil {
		return nil, fmt.Errorf("amqp_poller: dial %s: %w", parsed.Redacted(), err)
	}

	var ch *amqp.Channel
	err = amqpCall(context.Background(), raw, func() error {
		ch, err = conn.Channel()
		return err
	})
	if err != nil {
		_ = conn.CloseDeadline(time.Now().Add(amqpOperationTimeout))
		return nil, fmt.Errorf("amqp_poller: open channel: %w", err)
	}

	batchMax := int(t.BatchSizeMax)
	if batchMax <= 0 {
		batchMax = 64
	}

	return &amqpPoller{
		op:       &realAMQPBrokerOp{conn: conn, ch: ch, raw: raw},
		queue:    cfg.Queue,
		batchMax: batchMax,
		inFlight: make(map[string]uint64),
	}, nil
}

func (p *amqpPoller) Kind() string {
	return "amqp"
}

func (p *amqpPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	ctx, cancel := context.WithTimeout(ctx, amqpOperationTimeout)
	defer cancel()
	if p == nil || p.op == nil {
		return PollResult{Error: errors.New("amqp_poller: uninitialized")}
	}
	batchMax := p.batchMax
	if t.BatchSizeMax > 0 {
		batchMax = int(t.BatchSizeMax)
	}

	var records []SourceRecord
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := 0; i < batchMax; i++ {
		select {
		case <-ctx.Done():
			return PollResult{Records: records, Error: ctx.Err()}
		default:
		}

		msg, ok, err := p.op.Get(ctx, p.queue, false)
		if err != nil {
			return PollResult{Records: records, Error: err}
		}
		if !ok {
			break
		}

		id := strconv.FormatUint(msg.DeliveryTag, 10)
		p.inFlight[id] = msg.DeliveryTag

		headers := make(map[string]string)
		for k, v := range msg.Headers {
			headers[k] = fmt.Sprintf("%v", v)
		}
		if msg.ContentType != "" {
			headers["content-type"] = msg.ContentType
		}
		if msg.MessageId != "" {
			headers["message-id"] = msg.MessageId
		}

		records = append(records, SourceRecord{
			ItemIdentifier:   id,
			StableIdentifier: msg.MessageId,
			Payload:          msg.Body,
			Headers:          headers,
			ReceivedAt:       msg.Timestamp,
			Metadata: map[string]any{
				"routing_key":  msg.RoutingKey,
				"exchange":     msg.Exchange,
				"delivery_tag": id,
			},
		})
	}

	return PollResult{Records: records}
}

func (p *amqpPoller) Ack(ctx context.Context, _ sqlc.Trigger, ids []string) error {
	ctx, cancel := context.WithTimeout(ctx, amqpOperationTimeout)
	defer cancel()
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for _, id := range ids {
		tag, ok := p.inFlight[id]
		if !ok {
			continue
		}
		if err := p.op.Ack(ctx, tag, false); err != nil {
			errs = append(errs, fmt.Errorf("ack %s: %w", id, err))
		}
		delete(p.inFlight, id)
	}
	return errors.Join(errs...)
}

func (p *amqpPoller) Nack(ctx context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, amqpOperationTimeout)
	defer cancel()
	p.mu.Lock()
	defer p.mu.Unlock()

	requeue := reason != "poison_record"
	var errs []error
	for _, id := range ids {
		tag, ok := p.inFlight[id]
		if !ok {
			continue
		}
		if err := p.op.Nack(ctx, tag, false, requeue); err != nil {
			errs = append(errs, fmt.Errorf("nack %s: %w", id, err))
		}
		delete(p.inFlight, id)
	}
	return errors.Join(errs...)
}

func (p *amqpPoller) BrokerStats(ctx context.Context, _ sqlc.Trigger) BrokerStats {
	if p == nil || p.op == nil {
		return BrokerStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	q, err := p.op.QueueInspect(ctx, p.queue)
	if err != nil {
		return BrokerStats{}
	}
	return BrokerStats{
		Lag:       int64(q.Messages),
		Depth:     int64(q.Messages),
		Available: true,
	}
}

func (p *amqpPoller) Close() error {
	if p == nil || p.op == nil {
		return nil
	}
	return p.op.Close()
}

// A timeout closes the transport. Let the scheduler replace that connection
// on a subsequent tick instead of retaining a permanently failed poller.
func (p *amqpPoller) Closed() bool {
	if p == nil || p.op == nil {
		return true
	}
	op, ok := p.op.(*realAMQPBrokerOp)
	return ok && op.conn.IsClosed()
}

func init() {
	registerPoller("amqp", newAMQPPoller)
	registerPoller("rabbitmq", newAMQPPoller)
}
