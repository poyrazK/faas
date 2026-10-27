package agentd

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// The delivery worker (ADR-20261027090010 §8, ADR-20261027090014 §1). It claims due deliveries
// under a lease token, writes each to its destination, and records how the
// attempt ended; the store ends the run with its last delivery. GitHub
// comments go through githubd, which holds the installation token.

// DeliveryStore is the agent_deliveries surface the worker writes;
// *agentstore.Store implements it.
type DeliveryStore interface {
	ClaimDeliveries(ctx context.Context, token string, ttl time.Duration, limit int) ([]agentstore.DeliveryClaim, []agentstore.DeliveryEnd, error)
	EndDeliveryAttempt(ctx context.Context, c agentstore.DeliveryClaim, r agentstore.DeliveryResult) (string, error)
	SettleDeliveringRuns(ctx context.Context, limit int) (int, error)
}

// Commenter writes marker comments; *githubdgrpc.Client implements it.
type Commenter interface {
	UpsertIssueComment(ctx context.Context, account string, installation int64, repository string, number int64, markerKey, body string) (githubdgrpc.IssueComment, error)
}

// DeliveryConfig tunes the worker.
type DeliveryConfig struct {
	// LeaseTTL bounds one attempt; a worker that vanishes mid-attempt leaves
	// the delivery to be reconciled once it lapses.
	LeaseTTL time.Duration
	// Batch bounds the deliveries claimed, and written in parallel, at once.
	Batch int
	// PollInterval is the wait after a claim found nothing due.
	PollInterval time.Duration
}

func (c DeliveryConfig) withDefaults() DeliveryConfig {
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = time.Minute
	}
	if c.Batch <= 0 {
		c.Batch = 8
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	return c
}

// settleSweepEvery is how many polls pass between sweeps for delivering
// runs that no delivery settled.
const settleSweepEvery = 30

// recordTimeout bounds recording an attempt after the worker's context ends.
const recordTimeout = 5 * time.Second

// Deliverer is one replica's delivery worker.
type Deliverer struct {
	store   DeliveryStore
	github  Commenter
	buckets *Buckets
	cfg     DeliveryConfig
	log     *slog.Logger
	metrics *Metrics
}

// NewDeliverer returns a worker. github may be nil; GitHub comments then
// wait and retry until their window closes.
func NewDeliverer(store DeliveryStore, github Commenter, cfg DeliveryConfig, log *slog.Logger) *Deliverer {
	if log == nil {
		log = slog.Default()
	}
	return &Deliverer{store: store, github: github, cfg: cfg.withDefaults(), log: log}
}

// WithBuckets lets the worker write bucket deliveries. Without it they fail
// as buckets_unavailable.
func (d *Deliverer) WithBuckets(b *Buckets) *Deliverer {
	d.buckets = b
	return d
}

// WithMetrics counts deliveries and attempts.
func (d *Deliverer) WithMetrics(m *Metrics) *Deliverer {
	d.metrics = m
	return d
}

// Run delivers until ctx ends.
func (d *Deliverer) Run(ctx context.Context) {
	for polls := 0; ; polls++ {
		n, err := d.DeliverOnce(ctx)
		if err != nil && ctx.Err() == nil {
			d.log.Warn("agentd: deliveries", "err", err)
		}
		if polls%settleSweepEvery == 0 {
			if settled, err := d.store.SettleDeliveringRuns(ctx, 100); err != nil && ctx.Err() == nil {
				d.log.Warn("agentd: settle delivering runs", "err", err)
			} else if settled > 0 {
				d.log.Info("agentd: settled delivering runs", "runs", settled)
			}
		}
		if n == 0 && !sleep(ctx, d.cfg.PollInterval) {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// DeliverOnce claims due deliveries, writes them in parallel and records
// each attempt. It returns how many it claimed.
func (d *Deliverer) DeliverOnce(ctx context.Context) (int, error) {
	claims, ends, err := d.store.ClaimDeliveries(ctx, uuid.NewString(), d.cfg.LeaseTTL, d.cfg.Batch)
	if err != nil {
		return 0, err
	}
	for _, e := range ends {
		d.metrics.ended(e)
	}
	var wg sync.WaitGroup
	for _, c := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			result := d.deliver(ctx, c)
			took := time.Since(start)
			recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
			defer stop()
			state, err := d.store.EndDeliveryAttempt(recordCtx, c, result)
			if err != nil {
				d.log.Warn("agentd: record delivery", "delivery_id", c.ID, "run_id", c.RunID, "err", err)
				return
			}
			d.metrics.attempted(c.Destination, result, state, took)
			d.log.Info("agentd: delivery attempt", "delivery_id", c.ID, "run_id", c.RunID, "destination", c.Destination,
				"attempt", c.Attempt, "outcome", result.Code)
		}()
	}
	wg.Wait()
	return len(claims), nil
}

// deliver makes one attempt, bounded well inside the lease.
func (d *Deliverer) deliver(ctx context.Context, c agentstore.DeliveryClaim) agentstore.DeliveryResult {
	ctx, stop := context.WithTimeout(ctx, d.cfg.LeaseTTL*2/3)
	defer stop()
	switch c.Destination {
	case api.AgentDeliveryGitHubComment:
		if d.github == nil {
			return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRetry, Code: "github_unavailable",
				Message: "this agentd replica cannot reach githubd"}
		}
		comment, err := d.github.UpsertIssueComment(ctx, c.AccountID, c.InstallationID, c.Repository, c.Number, c.Key, c.Body)
		if err == nil {
			return agentstore.DeliveryResult{Outcome: agentstore.DeliveryDelivered, Code: "delivered",
				ExternalID: strconv.FormatInt(comment.ID, 10), URL: comment.URL}
		}
		return classifyGitHub(err)
	case api.AgentDeliveryBucket:
		if d.buckets == nil {
			return rejected("buckets_unavailable", "this agentd replica cannot write buckets")
		}
		return d.buckets.deliver(ctx, c)
	default:
		return rejected("unknown_destination", "agentd cannot deliver to "+c.Destination)
	}
}

// classifyGitHub maps githubd's answer to an attempt outcome. A refusal is
// final; nothing written is retried; anything else may have written, so the
// next attempt reconciles against the issue first.
func classifyGitHub(err error) agentstore.DeliveryResult {
	st, _ := status.FromError(err)
	message := st.Message()
	switch st.Code() {
	case codes.NotFound:
		return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRejected, Code: "github_installation_missing", Message: message}
	case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied:
		return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRejected, Code: "github_rejected", Message: message}
	case codes.Unavailable, codes.ResourceExhausted, codes.Unimplemented:
		return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRetry, Code: "github_unavailable", Message: message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		message = "githubd did not answer in time"
	}
	return agentstore.DeliveryResult{Outcome: agentstore.DeliveryUnknown, Code: "github_outcome_unknown", Message: message}
}
