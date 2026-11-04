package sched

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	kafka "github.com/segmentio/kafka-go"
)

// XRedisTLSDial is a Redis Streams trigger's dialer over dial instead of
// the egress-guarded one, so a test can reach a loopback server.
// adr: 20261101104512 — a `tls: true` Redis trigger dials TLS.
func XRedisTLSDial(dial func(context.Context, string, string) (net.Conn, error), cfg *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	return redisTLSDial(dial, cfg)
}

// XRedisTriggerOptions is the client configuration of a Redis Streams
// trigger with the given address and TLS flag.
func XRedisTriggerOptions(addr string, useTLS bool) *redis.Options {
	return redisTriggerOptions(redisConfig{Addr: addr, Stream: "s", Group: "g", TLS: useTLS})
}

// KafkaLagClient is the broker surface the Kafka lag reader uses.
type KafkaLagClient = kafkaLagClient

// XKafkaGroupLag sums a group's per-partition lag.
// adr: 20261101104512 — Kafka lag is read from the committed offsets.
func XKafkaGroupLag(committed map[int]int64, watermarks []kafka.PartitionOffsets) (int64, error) {
	return kafkaGroupLag(committed, watermarks)
}

// XNewKafkaPollerWithLag is a Kafka poller over reader whose lag comes from
// client and whose consumer-group errors are whatever logf is handed.
func XNewKafkaPollerWithLag(reader KafkaBrokerOp, client KafkaLagClient, topic, group string, now func() time.Time) (TriggerSource, func(format string, args ...any)) {
	health := &kafkaGroupHealth{now: now}
	poller := &kafkaPoller{reader: reader, batchMax: 100, inFlight: map[string]kafka.Message{},
		open: map[int]map[int64]bool{}, settled: map[int][]kafka.Message{},
		lag:   &kafkaLagReader{client: client, topic: topic, group: group, now: now},
		group: health,
	}
	return poller, health.logger().Printf
}

// XNATSBrokerPool is a NATS broker pool over dial with the given idle
// timeout, which takes a broker as closed when isClosed says so.
// adr: 20261101104512 — NATS connections are dialed outside the pool lock,
// closed when idle, and replaced when nats.go has closed them.
func XNATSBrokerPool(dial func(rawURL string) (*NATSBroker, error), idle time.Duration, isClosed func(*NATSBroker) bool) *natsBrokerPool {
	p := newNATSBrokerPool(dial, idle)
	p.isClosed = isClosed
	return p
}

// Acquire takes a reference to rawURL's broker.
func (p *natsBrokerPool) Acquire(rawURL string) (*NATSBroker, func(), error) {
	return p.acquire(rawURL)
}

// Open reports whether the pool holds a broker for rawURL.
func (p *natsBrokerPool) Open(rawURL string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.brokers[natsBrokerKey(rawURL)]
	return ok
}
