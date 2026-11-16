package faas

// adr: 20261113180000 — a pull consumer for Go workers (R5-12). The Go SDK
// had only the raw lease verbs, so every Go worker re-implemented lease
// extension, settling and back-off; the Node and Python SDKs ship a consumer.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gregale-dev/gregale-go/internal/api"
)

// QueueLeaseMessage is one leased queue message.
type QueueLeaseMessage = api.QueueLeaseMessage

// QueueHandler handles one leased message. Returning nil acknowledges it; an
// error returns it for a retry under its retry policy. ctx is cancelled when
// the lease is lost, after which the message belongs to its next delivery.
type QueueHandler func(ctx context.Context, msg QueueLeaseMessage) error

// QueueConsumerOptions configures ConsumeQueue. App and Queue are required.
type QueueConsumerOptions struct {
	// Organization leases through the organization's URLs when set.
	Organization string
	App          string
	Queue        string
	// Concurrency is how many messages are handled at once (default 1).
	Concurrency int
	// VisibilityTimeout is each lease's timeout, extended while the handler
	// runs (default 30 s).
	VisibilityTimeout time.Duration
	// Wait is how long one lease call waits for a message (default and
	// maximum 20 s).
	Wait time.Duration
	// RetryDelay delays a failed message's next delivery.
	RetryDelay time.Duration
	// OnError receives errors the consumer recovers from.
	OnError func(error)
}

const (
	queueConsumerMaxWait     = 20 * time.Second
	queueConsumerMaxBackoff  = 30 * time.Second
	queueConsumerMaxBatch    = 100
	queueConsumerErrorBytes  = 1024
	queueConsumerEmptyPacing = time.Second
)

var queueSettleRetryDelays = []time.Duration{500 * time.Millisecond, 2 * time.Second}

// ConsumeQueue leases and handles the queue's messages until ctx is done,
// then lets the handlers in flight finish and settles them before it
// returns.
func (c *Client) ConsumeQueue(ctx context.Context, opts QueueConsumerOptions, handler QueueHandler) error {
	if opts.App == "" || opts.Queue == "" || handler == nil {
		return errors.New("queue consumer requires App, Queue and a handler")
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.VisibilityTimeout <= 0 {
		opts.VisibilityTimeout = 30 * time.Second
	}
	if opts.Wait <= 0 || opts.Wait > queueConsumerMaxWait {
		opts.Wait = queueConsumerMaxWait
	}
	q := queueConsumer{client: c, opts: opts, handler: handler, slots: make(chan struct{}, opts.Concurrency)}
	return q.run(ctx)
}

type queueConsumer struct {
	client  *Client
	opts    QueueConsumerOptions
	handler QueueHandler
	slots   chan struct{}
	wg      sync.WaitGroup
}

func (q *queueConsumer) run(ctx context.Context) error {
	defer q.wg.Wait()
	backoff := time.Duration(0)
	for {
		// Wait for one free slot, then take every other free one.
		select {
		case <-ctx.Done():
			return nil
		case q.slots <- struct{}{}:
		}
		free := 1
		for free < queueConsumerMaxBatch {
			select {
			case q.slots <- struct{}{}:
				free++
				continue
			default:
			}
			break
		}
		started := time.Now()
		resp, err := q.lease(ctx, free)
		if err != nil {
			q.release(free)
			if ctx.Err() != nil {
				return nil
			}
			q.report(err)
			backoff = min(max(backoff*2, time.Second), queueConsumerMaxBackoff)
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			continue
		}
		backoff = 0
		q.release(free - len(resp.Messages))
		// An empty answer that came back early (the server's waiter cap,
		// or a zero wait) is paced, so the loop never spins.
		if len(resp.Messages) == 0 && time.Since(started) < queueConsumerEmptyPacing && !sleepCtx(ctx, queueConsumerEmptyPacing) {
			return nil
		}
		for _, msg := range resp.Messages {
			q.wg.Add(1)
			go func(msg QueueLeaseMessage) {
				defer q.wg.Done()
				defer q.release(1)
				q.process(ctx, msg)
			}(msg)
		}
	}
}

func (q *queueConsumer) release(n int) {
	for i := 0; i < n; i++ {
		<-q.slots
	}
}

func (q *queueConsumer) process(ctx context.Context, msg QueueLeaseMessage) {
	// The handler outlives a stop of the consumer, as the other SDKs' do,
	// and is cancelled only when its lease is lost.
	handlerCtx, lose := context.WithCancelCause(context.WithoutCancel(ctx))
	defer lose(nil)
	done := make(chan struct{})
	go q.extend(handlerCtx, msg.Receipt, lose, done)
	err := q.handler(handlerCtx, msg)
	close(done)
	if handlerCtx.Err() != nil {
		return // the lease was lost: the message belongs to its next delivery now
	}
	settle := func() (bool, error) {
		if err == nil {
			return q.settle(ctx, ":ack", api.QueueLeaseReceiptsRequest{Receipts: []string{msg.Receipt}})
		}
		q.report(err)
		return q.settle(ctx, ":nack", api.QueueLeaseReceiptsRequest{Receipts: []string{msg.Receipt},
			Error: truncateQueueError(err.Error()), DelaySeconds: int(q.opts.RetryDelay / time.Second)})
	}
	for attempt := 0; ; attempt++ {
		held, settleErr := settle()
		if settleErr == nil {
			if !held {
				q.report(fmt.Errorf("queue lease for message %s expired before it was settled", msg.ID))
			}
			return
		}
		if attempt >= len(queueSettleRetryDelays) {
			q.report(settleErr)
			return
		}
		time.Sleep(queueSettleRetryDelays[attempt])
	}
}

// extend renews the lease every third of its timeout while the handler runs.
// One failed extend still leaves a retry before the lease runs out; once none
// has held for a whole timeout the lease has lapsed and the handler is told.
func (q *queueConsumer) extend(ctx context.Context, receipt string, lose context.CancelCauseFunc, done <-chan struct{}) {
	ticker := time.NewTicker(q.opts.VisibilityTimeout / 3)
	defer ticker.Stop()
	heldAt := time.Now()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		if time.Since(heldAt) >= q.opts.VisibilityTimeout {
			lose(errors.New("queue lease lost: extensions failed until it expired"))
			return
		}
		held, err := q.settle(ctx, ":extend", api.QueueLeaseReceiptsRequest{Receipts: []string{receipt},
			VisibilityTimeoutSeconds: int(q.opts.VisibilityTimeout / time.Second)})
		switch {
		case err != nil:
			q.report(err)
		case held:
			heldAt = time.Now()
		default:
			lose(errors.New("queue lease lost"))
			return
		}
	}
}

func (q *queueConsumer) lease(ctx context.Context, n int) (api.QueueLeaseResponse, error) {
	req := api.QueueLeaseRequest{MaxMessages: n, VisibilityTimeoutSeconds: int(q.opts.VisibilityTimeout / time.Second),
		WaitSeconds: int(q.opts.Wait / time.Second)}
	if q.opts.Organization != "" {
		return q.client.LeaseOrgQueueMessages(ctx, q.opts.Organization, q.opts.App, q.opts.Queue, req)
	}
	return q.client.LeaseQueueMessages(ctx, q.opts.App, q.opts.Queue, req)
}

// settle applies one verb and reports whether the receipt still held its
// message. It runs detached from the consumer's stop, which must not strand
// a handled message.
func (q *queueConsumer) settle(ctx context.Context, verb string, req api.QueueLeaseReceiptsRequest) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	org, app, queue := q.opts.Organization, q.opts.App, q.opts.Queue
	var resp api.QueueLeaseReceiptsResponse
	var err error
	switch {
	case verb == ":ack" && org != "":
		resp, err = q.client.AckOrgQueueLeases(ctx, org, app, queue, req)
	case verb == ":ack":
		resp, err = q.client.AckQueueLeases(ctx, app, queue, req)
	case verb == ":nack" && org != "":
		resp, err = q.client.NackOrgQueueLeases(ctx, org, app, queue, req)
	case verb == ":nack":
		resp, err = q.client.NackQueueLeases(ctx, app, queue, req)
	case org != "":
		resp, err = q.client.ExtendOrgQueueLeases(ctx, org, app, queue, req)
	default:
		resp, err = q.client.ExtendQueueLeases(ctx, app, queue, req)
	}
	if err != nil {
		return false, err
	}
	for _, receipt := range resp.Succeeded {
		if len(req.Receipts) > 0 && receipt == req.Receipts[0] {
			return true, nil
		}
	}
	return false, nil
}

func (q *queueConsumer) report(err error) {
	if q.opts.OnError != nil && err != nil {
		q.opts.OnError(err)
	}
}

func truncateQueueError(text string) string {
	if len(text) <= queueConsumerErrorBytes {
		return text
	}
	cut := queueConsumerErrorBytes
	for cut > 0 && text[cut]&0xC0 == 0x80 { // keep the UTF-8 sequence whole
		cut--
	}
	return text[:cut]
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
