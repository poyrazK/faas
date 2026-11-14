package alerts

import (
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// webhookPool runs one account's webhook deliveries off its evaluation path
// (ADR-20261110090000). A delivery and its retries take up to
// AlertWebhookBudget; evaluated in line, one failing receiver held every
// later rule of the account that long, and two used up its tick budget so the
// rest waited a whole tick. Rules are still evaluated (and their actions run)
// one at a time; only sending the webhook moves here. At most
// AlertAccountWebhookConcurrency deliveries run at once, and the account's
// evaluation waits for them before it returns, so a tick's counters and
// delivery rows are complete when the tick ends.
type webhookPool struct {
	slots chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	stats Stats
}

type webhookPoolKey struct{}

func newWebhookPool() *webhookPool {
	return &webhookPool{slots: make(chan struct{}, api.AlertAccountWebhookConcurrency)}
}

func withWebhookPool(ctx context.Context, p *webhookPool) context.Context {
	return context.WithValue(ctx, webhookPoolKey{}, p)
}

// send runs deliver on the account's pool when ctx carries one, else inline
// (a single-rule caller, or a test driving evalRule directly). deliver
// records into the Stats it is given.
func (e *Evaluator) send(ctx context.Context, stats *Stats, deliver func(*Stats)) {
	p, _ := ctx.Value(webhookPoolKey{}).(*webhookPool)
	if p == nil {
		deliver(stats)
		return
	}
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		deliver(stats) // records the cut-short delivery as failed
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.slots }()
		var local Stats
		deliver(&local)
		p.mu.Lock()
		p.stats.add(local)
		p.mu.Unlock()
	}()
}

// wait blocks until every delivery the pool started has been recorded and
// returns their counters.
func (p *webhookPool) wait() Stats {
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}
