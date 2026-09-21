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
	QueueInspect(name string) (amqp.Queue, error)
	Get(queue string, autoAck bool) (amqp.Delivery, bool, error)
	Ack(tag uint64, multiple bool) error
	Nack(tag uint64, multiple bool, requeue bool) error
	Close() error
}

type realAMQPBrokerOp struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func (r *realAMQPBrokerOp) QueueInspect(name string) (amqp.Queue, error) {
	return r.ch.QueueDeclarePassive(name, false, false, false, false, nil)
}

func (r *realAMQPBrokerOp) Get(queue string, autoAck bool) (amqp.Delivery, bool, error) {
	return r.ch.Get(queue, autoAck)
}

func (r *realAMQPBrokerOp) Ack(tag uint64, multiple bool) error {
	return r.ch.Ack(tag, multiple)
}

func (r *realAMQPBrokerOp) Nack(tag uint64, multiple bool, requeue bool) error {
	return r.ch.Nack(tag, multiple, requeue)
}

func (r *realAMQPBrokerOp) Close() error {
	var errs []error
	if r.ch != nil {
		if err := r.ch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.conn != nil {
		if err := r.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	amqpCfg := amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return dialContext(ctx, network, addr)
		},
	}
	if parsed.Scheme == "amqps" {
		amqpCfg.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	conn, err := amqp.DialConfig(cfg.URL, amqpCfg)
	if err != nil {
		return nil, fmt.Errorf("amqp_poller: dial %s: %w", parsed.Redacted(), err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("amqp_poller: open channel: %w", err)
	}

	batchMax := int(t.BatchSizeMax)
	if batchMax <= 0 {
		batchMax = 64
	}

	return &amqpPoller{
		op:       &realAMQPBrokerOp{conn: conn, ch: ch},
		queue:    cfg.Queue,
		batchMax: batchMax,
		inFlight: make(map[string]uint64),
	}, nil
}

func (p *amqpPoller) Kind() string {
	return "amqp"
}

func (p *amqpPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
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

		msg, ok, err := p.op.Get(p.queue, false)
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
			ItemIdentifier: id,
			Payload:        msg.Body,
			Headers:        headers,
			ReceivedAt:     msg.Timestamp,
			Metadata: map[string]any{
				"routing_key":  msg.RoutingKey,
				"exchange":     msg.Exchange,
				"delivery_tag": id,
			},
		})
	}

	return PollResult{Records: records}
}

func (p *amqpPoller) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for _, id := range ids {
		tag, ok := p.inFlight[id]
		if !ok {
			continue
		}
		if err := p.op.Ack(tag, false); err != nil {
			errs = append(errs, fmt.Errorf("ack %s: %w", id, err))
		}
		delete(p.inFlight, id)
	}
	return errors.Join(errs...)
}

func (p *amqpPoller) Nack(_ context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	requeue := reason != "poison_record"
	var errs []error
	for _, id := range ids {
		tag, ok := p.inFlight[id]
		if !ok {
			continue
		}
		if err := p.op.Nack(tag, false, requeue); err != nil {
			errs = append(errs, fmt.Errorf("nack %s: %w", id, err))
		}
		delete(p.inFlight, id)
	}
	return errors.Join(errs...)
}

func (p *amqpPoller) BrokerStats(_ context.Context, _ sqlc.Trigger) BrokerStats {
	if p == nil || p.op == nil {
		return BrokerStats{}
	}
	q, err := p.op.QueueInspect(p.queue)
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

func init() {
	registerPoller("amqp", newAMQPPoller)
	registerPoller("rabbitmq", newAMQPPoller)
}
