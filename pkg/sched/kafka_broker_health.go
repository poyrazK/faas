// kafka_broker_health.go — what a Kafka trigger reports about its broker
// (ADR-20261101104512): consumer lag read from the group's committed offsets,
// and consumer-group failures kafka-go would otherwise swallow.

package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// kafkaLagCacheTTL bounds how often one trigger asks its broker for lag:
// the health writer and queue_lag autoscaling both read it.
const kafkaLagCacheTTL = 10 * time.Second

// kafkaLagTimeout bounds one lag reading (three broker round trips).
const kafkaLagTimeout = 5 * time.Second

// kafkaGroupErrorWindow is how long a consumer-group error logged by
// kafka-go keeps an empty poll failing. While a group cannot join (bad
// credentials, an unreachable coordinator) kafka-go retries and logs every
// few seconds, so the error stays fresh; once it joins, the error ages out.
const kafkaGroupErrorWindow = 30 * time.Second

// kafkaLagReader reads a consumer group's lag from the broker. The reader's
// own Stats().Lag is one gauge shared by every partition, measured from the
// fetch position rather than the committed offset, so it read low and never
// showed a commit that had stopped moving (a record held for retry pins its
// partition's commit). This one sums, per partition, the high watermark less
// the group's committed offset, or less the log start where the group has
// committed nothing, since a new group starts at the first offset.
type kafkaLagReader struct {
	client    kafkaLagClient
	transport *kafka.Transport
	topic     string
	group     string
	now       func() time.Time

	mu     sync.Mutex
	cached BrokerStats
	at     time.Time
}

// kafkaLagClient is the part of *kafka.Client the lag reader uses.
type kafkaLagClient interface {
	Metadata(context.Context, *kafka.MetadataRequest) (*kafka.MetadataResponse, error)
	OffsetFetch(context.Context, *kafka.OffsetFetchRequest) (*kafka.OffsetFetchResponse, error)
	ListOffsets(context.Context, *kafka.ListOffsetsRequest) (*kafka.ListOffsetsResponse, error)
}

// newKafkaLagReader dials the trigger's brokers the way its reader does:
// through the egress guard, with its TLS and SASL.
func newKafkaLagReader(cfg kafkaConfig, dialer *kafka.Dialer) *kafkaLagReader {
	transport := &kafka.Transport{
		Dial:        dialer.DialFunc,
		DialTimeout: dialer.Timeout,
		IdleTimeout: 30 * time.Second,
		TLS:         dialer.TLS,
		SASL:        dialer.SASLMechanism,
		ClientID:    "gregale-schedd",
	}
	return &kafkaLagReader{
		client:    &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: kafkaLagTimeout, Transport: transport},
		transport: transport,
		topic:     cfg.Topic,
		group:     cfg.Group,
		now:       time.Now,
	}
}

// stats returns the group's lag, read at most every kafkaLagCacheTTL.
func (r *kafkaLagReader) stats(ctx context.Context) BrokerStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if !r.at.IsZero() && now.Sub(r.at) < kafkaLagCacheTTL {
		return r.cached
	}
	ctx, cancel := context.WithTimeout(ctx, kafkaLagTimeout)
	defer cancel()
	lag, err := r.read(ctx)
	r.cached = BrokerStats{Lag: lag, Depth: lag, Available: err == nil}
	r.at = now
	return r.cached
}

func (r *kafkaLagReader) read(ctx context.Context) (int64, error) {
	meta, err := r.client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{r.topic}})
	if err != nil {
		return 0, fmt.Errorf("kafka_poller: lag metadata: %w", err)
	}
	var partitions []int
	for _, topic := range meta.Topics {
		if topic.Name != r.topic {
			continue
		}
		if topic.Error != nil {
			return 0, fmt.Errorf("kafka_poller: lag metadata %s: %w", r.topic, topic.Error)
		}
		for _, p := range topic.Partitions {
			partitions = append(partitions, p.ID)
		}
	}
	if len(partitions) == 0 {
		return 0, fmt.Errorf("kafka_poller: lag: topic %s has no partitions", r.topic)
	}
	fetched, err := r.client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: r.group, Topics: map[string][]int{r.topic: partitions}})
	if err != nil {
		return 0, fmt.Errorf("kafka_poller: lag committed offsets: %w", err)
	}
	if fetched.Error != nil {
		return 0, fmt.Errorf("kafka_poller: lag committed offsets: %w", fetched.Error)
	}
	committed := make(map[int]int64, len(partitions))
	for _, p := range fetched.Topics[r.topic] {
		if p.Error != nil {
			return 0, fmt.Errorf("kafka_poller: lag committed offset of partition %d: %w", p.Partition, p.Error)
		}
		committed[p.Partition] = p.CommittedOffset
	}
	requests := make([]kafka.OffsetRequest, 0, 2*len(partitions))
	for _, p := range partitions {
		requests = append(requests, kafka.FirstOffsetOf(p), kafka.LastOffsetOf(p))
	}
	listed, err := r.client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{r.topic: requests}})
	if err != nil {
		return 0, fmt.Errorf("kafka_poller: lag watermarks: %w", err)
	}
	return kafkaGroupLag(committed, listed.Topics[r.topic])
}

// kafkaGroupLag sums each partition's high watermark less where the group
// resumes: its committed offset, or the log start when it has none (or the
// log was truncated past it).
func kafkaGroupLag(committed map[int]int64, watermarks []kafka.PartitionOffsets) (int64, error) {
	if len(watermarks) == 0 {
		return 0, errors.New("kafka_poller: lag: no watermarks")
	}
	var lag int64
	for _, p := range watermarks {
		if p.Error != nil {
			return 0, fmt.Errorf("kafka_poller: lag watermark of partition %d: %w", p.Partition, p.Error)
		}
		high := p.LastOffset
		start, ok := committed[p.Partition]
		if !ok || start < 0 || (p.FirstOffset >= 0 && start < p.FirstOffset) {
			start = max(p.FirstOffset, 0)
		}
		if high > start {
			lag += high - start
		}
	}
	return lag, nil
}

func (r *kafkaLagReader) close() {
	if r != nil && r.transport != nil {
		r.transport.CloseIdleConnections()
	}
}

// BrokerStats reports the consumer group's lag across the topic's
// partitions, from the committed offsets.
func (k *kafkaPoller) BrokerStats(ctx context.Context, _ sqlc.Trigger) BrokerStats {
	if k == nil || k.lag == nil {
		return BrokerStats{Available: false}
	}
	return k.lag.stats(ctx)
}

// kafkaGroupHealth keeps the consumer-group errors kafka-go logs. kafka-go
// hands a group failure (bad credentials, an unreachable coordinator) to
// FetchMessage only when a fetch happens to be waiting as its retries give
// up, so a poll of a group that cannot join looked like a healthy empty
// poll and the poll-error alert never fired.
type kafkaGroupHealth struct {
	mu   sync.Mutex
	now  func() time.Time
	last string
	at   time.Time
}

func newKafkaGroupHealth() *kafkaGroupHealth { return &kafkaGroupHealth{now: time.Now} }

// logger is the reader's ErrorLogger.
func (h *kafkaGroupHealth) logger() kafka.Logger {
	return kafka.LoggerFunc(func(format string, args ...any) { h.observe(fmt.Sprintf(format, args...)) })
}

// kafkaBenignLogs are kafka-go error-log lines that do not mean the
// consumer is failing.
var kafkaBenignLogs = []string{
	"the kafka reader is reading before the first offset",
	"the kafka reader is reading passed the last offset",
	"leave group failed",
	"context canceled",
}

func (h *kafkaGroupHealth) observe(line string) {
	for _, benign := range kafkaBenignLogs {
		if strings.Contains(line, benign) {
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last, h.at = line, h.now()
}

// fetched clears the error: a record arrived, so the consumer works.
func (h *kafkaGroupHealth) fetched() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = ""
}

// err is the error a poll that fetched nothing reports: the last one logged
// within kafkaGroupErrorWindow and not followed by a fetched record.
func (h *kafkaGroupHealth) err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == "" || h.now().Sub(h.at) > kafkaGroupErrorWindow {
		return nil
	}
	return fmt.Errorf("kafka_poller: consumer group: %s", h.last)
}

// pollHealth turns an empty poll into the group's error when it has one,
// and clears the error once a poll brings records.
func (k *kafkaPoller) pollHealth(res PollResult) PollResult {
	if k.group == nil || res.Error != nil {
		return res
	}
	if len(res.Records) > 0 {
		k.group.fetched()
		return res
	}
	if err := k.group.err(); err != nil {
		return PollResult{Error: err}
	}
	return res
}
