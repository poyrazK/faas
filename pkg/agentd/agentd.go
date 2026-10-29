// Package agentd runs agent runs (ADR-20261027090010 §4-§6, ADR-20261027090014 §1). Replicas
// claim queued runs with FOR UPDATE SKIP LOCKED under lease tokens, so no
// leader is needed; each claimed run goes to a schedd's RunAgent stream,
// which admits it on the agent pool and runs it through vmmd. agentd owns
// every agent_runs transition after queued and never touches instances or
// Firecracker.
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Store is the agent_runs surface agentd writes; *agentstore.Store
// implements it.
type Store interface {
	ClaimRun(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (agentstore.Claim, bool, error)
	MarkRunning(ctx context.Context, runID, token, nodeID, instanceID string, aiBudgetMillicents int64) error
	RecordBoot(ctx context.Context, runID, token string, boot time.Duration) error
	Renew(ctx context.Context, runID, token string, ttl time.Duration) (bool, error)
	Requeue(ctx context.Context, runID, token string) error
	FinishRun(ctx context.Context, accountID, runID, token string, f agentstore.Finish) error
	ExpireLeases(ctx context.Context) (int64, int64, error)
}

// Schedd opens RunAgent streams; scheddpb.ScheddClient implements it.
type Schedd interface {
	RunAgent(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[scheddpb.AgentRunRequest, scheddpb.AgentRunEvent], error)
}

// Config tunes one replica.
type Config struct {
	// Owner names this replica in lease_owner.
	Owner string
	// LeaseTTL bounds how long a run outlives a replica that stopped
	// renewing; renewals come every third of it.
	LeaseTTL time.Duration
	// Concurrency bounds the runs this replica drives at once.
	Concurrency int
	// PollInterval is the wait after an empty claim or a capacity refusal.
	PollInterval time.Duration
	// SweepInterval is how often expired leases are handled.
	SweepInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Owner == "" {
		c.Owner = "agentd"
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 30 * time.Second
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = 10 * time.Second
	}
	return c
}

// logTailBytes is how much of a run's output is kept with its outputs.
const logTailBytes = 64 << 10

// recvMaxBytes bounds one schedd event; the result carries inline outputs.
const recvMaxBytes = (api.AgentRunOutputFilesMaxBytes+agentrunproto.MaxResultBytes)*4/3 + 1<<20

// Agentd is one replica.
type Agentd struct {
	store     Store
	schedd    Schedd
	cfg       Config
	log       *slog.Logger
	checkouts *Checkouts
	deliverer *Deliverer
	outputs   *OutputFiles

	mu           sync.Mutex
	backoffUntil time.Time
}

// New returns a replica.
func New(store Store, schedd Schedd, cfg Config, log *slog.Logger) *Agentd {
	if log == nil {
		log = slog.Default()
	}
	return &Agentd{store: store, schedd: schedd, cfg: cfg.withDefaults(), log: log}
}

// WithCheckouts lets the replica prepare runs' checkouts (ADR-20261027090010 §3).
// Without it a run that asks for one fails with checkout_failed.
func (a *Agentd) WithCheckouts(c *Checkouts) *Agentd {
	a.checkouts = c
	return a
}

// WithOutputFiles keeps succeeded runs' output file contents (ADR-20261027090010 §3).
// Without it only their manifest is kept.
func (a *Agentd) WithOutputFiles(o *OutputFiles) *Agentd {
	a.outputs = o
	return a
}

// WithDeliveries lets the replica deliver succeeded runs (ADR-20261027090010 §8).
// Without it a run's declared deliveries fail as deliveries_unavailable.
func (a *Agentd) WithDeliveries(d *Deliverer) *Agentd {
	a.deliverer = d
	return a
}

// Run claims and drives runs until ctx ends, sweeps expired leases, and
// delivers when deliveries are on.
func (a *Agentd) Run(ctx context.Context) error {
	go a.sweep(ctx)
	if a.deliverer != nil {
		go a.deliverer.Run(ctx)
	}
	slots := make(chan struct{}, a.cfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		if wait := a.backoff(); wait > 0 {
			if !sleep(ctx, wait) {
				<-slots
				return nil
			}
		}
		claim, found, err := a.claim(ctx)
		if err != nil || !found {
			<-slots
			if err != nil && ctx.Err() == nil {
				a.log.Warn("agentd: claim failed", "err", err)
			}
			if !sleep(ctx, a.cfg.PollInterval) {
				return nil
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			a.drive(ctx, claim)
		}()
	}
}

// ClaimOnce claims one run and drives it to the end. It reports whether a
// run was claimed.
func (a *Agentd) ClaimOnce(ctx context.Context) (bool, error) {
	claim, found, err := a.claim(ctx)
	if err != nil || !found {
		return false, err
	}
	a.drive(ctx, claim)
	return true, nil
}

func (a *Agentd) claim(ctx context.Context) (agentstore.Claim, bool, error) {
	return a.store.ClaimRun(ctx, a.cfg.Owner, uuid.NewString(), a.cfg.LeaseTTL, func(plan string) int {
		p := api.Plan(plan)
		if !p.Valid() {
			return 0
		}
		return api.AgentRunConcurrentPerAccount[p.PlanIndex()]
	})
}

// SweepOnce handles expired leases once.
func (a *Agentd) SweepOnce(ctx context.Context) error {
	requeued, failed, err := a.store.ExpireLeases(ctx)
	if requeued+failed > 0 {
		a.log.Info("agentd: expired leases", "requeued", requeued, "failed", failed)
	}
	return err
}

func (a *Agentd) sweep(ctx context.Context) {
	tick := time.NewTicker(a.cfg.SweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := a.SweepOnce(ctx); err != nil && ctx.Err() == nil {
				a.log.Warn("agentd: sweep failed", "err", err)
			}
		}
	}
}

func (a *Agentd) backoff() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Until(a.backoffUntil)
}

func (a *Agentd) holdOff() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.backoffUntil = time.Now().Add(a.cfg.PollInterval)
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// run is one claimed run in flight.
type run struct {
	a       *Agentd
	claim   agentstore.Claim
	stream  grpc.BidiStreamingClient[scheddpb.AgentRunRequest, scheddpb.AgentRunEvent]
	sendMu  sync.Mutex
	mu      sync.Mutex
	running bool
	lost    bool
	cancel  bool
	tail    logTail
	stopRun context.CancelFunc
	// aiBudget is the run's model spend cap, recorded with the fence.
	aiBudget int64
}

// drive takes one claimed run from starting to a terminal state, or back to
// the queue if it never reached a VM.
func (a *Agentd) drive(ctx context.Context, claim agentstore.Claim) {
	machine, req, err := prepare(claim)
	if err != nil {
		a.finish(ctx, claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: "invalid_run", OutcomeMessage: err.Error()})
		return
	}
	var inputKey string
	if claim.Checkout != nil {
		key, checkout, err := a.prepareCheckout(ctx, claim)
		if err != nil {
			a.checkoutFailed(ctx, claim, err)
			return
		}
		inputKey, req.Checkout = key, &checkout
		if err := req.Validate(); err != nil {
			a.finish(ctx, claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: "invalid_run", OutcomeMessage: err.Error()})
			return
		}
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := a.schedd.RunAgent(runCtx, grpc.MaxCallRecvMsgSize(recvMaxBytes))
	if err != nil {
		a.requeue(ctx, claim, err)
		return
	}
	r := &run{a: a, claim: claim, stream: stream, stopRun: stop, aiBudget: aiBudget(claim)}
	body, err := json.Marshal(req)
	if err == nil {
		err = r.send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Start{Start: &scheddpb.AgentRunStart{
			RunId: claim.RunID, AccountId: claim.AccountID, Plan: claim.Plan, RamMb: int32(machine.RAMMB),
			Vcpu: int32(machine.VCPU), CpuMillicores: int32(machine.CPUMillicores), Run: body,
			Relay: &scheddpb.AgentRunRelay{LeaseToken: claim.LeaseToken, TenantId: claim.TenantID, AgentId: claim.AgentID,
				AgentVersion: int32(claim.AgentVersion)},
			InputKey: inputKey,
		}}})
	}
	if err != nil {
		a.requeue(ctx, claim, err)
		return
	}
	go r.keepLease(runCtx)
	r.receive(ctx)
}

// errCancelledBeforeBoot is a cancel that arrived while the checkout was
// prepared.
var errCancelledBeforeBoot = errors.New("agentd: cancelled before the run booted")

// prepareCheckout prepares the run's input drive while holding its lease,
// which a large fetch could otherwise outlast.
func (a *Agentd) prepareCheckout(ctx context.Context, claim agentstore.Claim) (string, agentrunproto.Checkout, error) {
	if a.checkouts == nil {
		return "", agentrunproto.Checkout{}, &CheckoutError{Code: outcomeCheckoutFailed, Err: errors.New("this agentd has no checkout source")}
	}
	prepCtx, stop := context.WithCancel(ctx)
	defer stop()
	held := make(chan error, 1)
	go func() { held <- a.holdLease(prepCtx, claim, stop) }()
	key, checkout, err := a.checkouts.Prepare(prepCtx, claim)
	stop()
	if leaseErr := <-held; leaseErr != nil {
		return "", agentrunproto.Checkout{}, leaseErr
	}
	return key, checkout, err
}

// holdLease renews a starting run's lease until ctx ends. It stops the
// caller when the lease is lost or a cancel is requested.
func (a *Agentd) holdLease(ctx context.Context, claim agentstore.Claim, stop context.CancelFunc) error {
	tick := time.NewTicker(a.cfg.LeaseTTL / 3)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		cancelRequested, err := a.store.Renew(ctx, claim.RunID, claim.LeaseToken, a.cfg.LeaseTTL)
		switch {
		case errors.Is(err, agentstore.ErrLeaseLost):
			stop()
			return agentstore.ErrLeaseLost
		case err != nil:
			a.log.Warn("agentd: renew failed", "run_id", claim.RunID, "err", err)
		case cancelRequested:
			stop()
			return errCancelledBeforeBoot
		}
	}
}

// checkoutFailed ends or requeues a run whose checkout was not prepared.
func (a *Agentd) checkoutFailed(ctx context.Context, claim agentstore.Claim, err error) {
	var final *CheckoutError
	switch {
	case errors.Is(err, agentstore.ErrLeaseLost):
		// Another replica owns the run now.
	case errors.Is(err, errCancelledBeforeBoot):
		a.finish(ctx, claim, agentstore.Finish{State: agentstore.StateCancelled, OutcomeCode: "cancelled"})
	case errors.As(err, &final):
		a.finish(ctx, claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: final.Code, OutcomeMessage: final.Err.Error()})
	default:
		a.requeue(ctx, claim, err)
	}
}

func (r *run) send(frame *scheddpb.AgentRunRequest) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	return r.stream.Send(frame)
}

// keepLease renews the lease every third of its TTL, forwards a requested
// cancel to schedd once, and ends the stream if the lease is lost.
func (r *run) keepLease(ctx context.Context) {
	tick := time.NewTicker(r.a.cfg.LeaseTTL / 3)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cancelRequested, err := r.a.store.Renew(ctx, r.claim.RunID, r.claim.LeaseToken, r.a.cfg.LeaseTTL)
		if errors.Is(err, agentstore.ErrLeaseLost) {
			r.mu.Lock()
			r.lost = true
			r.mu.Unlock()
			r.stopRun()
			return
		}
		if err != nil {
			r.a.log.Warn("agentd: renew failed", "run_id", r.claim.RunID, "err", err)
			continue
		}
		r.mu.Lock()
		first := cancelRequested && !r.cancel
		r.cancel = r.cancel || cancelRequested
		r.mu.Unlock()
		if first {
			_ = r.send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Cancel{Cancel: &scheddpb.AgentRunCancel{}}})
		}
	}
}

func (r *run) receive(ctx context.Context) {
	for {
		event, err := r.stream.Recv()
		if err != nil {
			r.streamEnded(ctx, err)
			return
		}
		switch {
		case event.GetPlaced() != nil:
			if !r.placed(ctx, event.GetPlaced()) {
				return
			}
		case event.GetBooted() != nil:
			boot := time.Duration(event.GetBooted().GetBootMs()) * time.Millisecond
			if err := r.a.store.RecordBoot(ctx, r.claim.RunID, r.claim.LeaseToken, boot); err != nil {
				r.a.log.Warn("agentd: record boot", "run_id", r.claim.RunID, "err", err)
			}
		case event.GetOutput() != nil:
			r.tail.write(event.GetOutput().GetChunk())
		case event.GetResult() != nil:
			r.result(ctx, event.GetResult().GetResult())
			return
		}
	}
}

// placed records the fence, then lets schedd boot the run.
func (r *run) placed(ctx context.Context, p *scheddpb.AgentRunPlaced) bool {
	if err := r.a.store.MarkRunning(ctx, r.claim.RunID, r.claim.LeaseToken, p.GetNodeId(), p.GetInstance(), r.aiBudget); err != nil {
		// Lost lease, or the store is down: nothing has booted, so stop
		// here; the sweeper queues the starting run again.
		r.a.log.Warn("agentd: mark running", "run_id", r.claim.RunID, "err", err)
		_ = r.send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Cancel{Cancel: &scheddpb.AgentRunCancel{}}})
		r.stopRun()
		return false
	}
	r.mu.Lock()
	r.running = true
	r.mu.Unlock()
	if err := r.send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}}); err != nil {
		r.streamEnded(ctx, err)
		return false
	}
	return true
}

func (r *run) result(ctx context.Context, body []byte) {
	var result agentrunproto.Result
	if err := json.Unmarshal(body, &result); err != nil || !result.Status.Valid() {
		r.a.finish(ctx, r.claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: "invalid_result",
			OutcomeMessage: "schedd returned an unreadable result", LogTail: r.tail.String(), LogTruncated: r.tail.dropped})
		return
	}
	exit, signal := int(result.ExitCode), int(result.Signal)
	f := agentstore.Finish{State: string(result.Status), OutcomeCode: result.FailureCode, OutcomeMessage: result.FailureMessage,
		ExitCode: &exit, Result: result.Result, LogTail: r.tail.String(), LogTruncated: result.LogTruncated || r.tail.dropped}
	if signal != 0 {
		f.Signal = &signal
	}
	if len(result.Files) > 0 && r.a.outputs != nil {
		files, err := r.a.outputs.Store(ctx, r.claim.AccountID, r.claim.RunID, result.Files)
		if err != nil {
			r.a.finish(ctx, r.claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: outcomeOutputsUnavailable,
				OutcomeMessage: err.Error(), ExitCode: &exit, LogTail: r.tail.String(), LogTruncated: f.LogTruncated})
			return
		}
		f.Files = files
	} else {
		for _, file := range result.Files {
			f.Files = append(f.Files, agentstore.OutputFile{Name: file.Name, Size: file.Size, SHA256: file.SHA256})
		}
	}
	r.a.finish(ctx, r.claim, f)
}

// streamEnded decides what an early end means. Before placed nothing booted:
// no capacity and transient failures queue the run again, a refusal fails
// it, a cancel before boot cancels it. After placed the run may have
// reached a VM, so it fails and is never replayed.
func (r *run) streamEnded(ctx context.Context, err error) {
	r.mu.Lock()
	running, lost, cancelled := r.running, r.lost, r.cancel
	r.mu.Unlock()
	if lost || ctx.Err() != nil {
		return // the lease or the process is gone; the sweeper decides
	}
	if errors.Is(err, io.EOF) {
		err = status.Error(codes.Unavailable, "schedd ended the stream without a result")
	}
	code := status.Code(err)
	switch {
	case cancelled && (code == codes.Aborted || !running):
		r.a.finish(ctx, r.claim, agentstore.Finish{State: agentstore.StateCancelled, OutcomeCode: "cancelled", LogTail: r.tail.String()})
	case running:
		// The seller sees a generic outcome; schedd's cause, which can name a
		// host path or a vmmd refusal, goes to the operator's log.
		r.a.log.Warn("agentd: run stream ended before its result", "run_id", r.claim.RunID, "err", err)
		r.a.finish(ctx, r.claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: "transport_failed",
			OutcomeMessage: "the run's stream ended before its result", LogTail: r.tail.String(), LogTruncated: r.tail.dropped})
	case code == codes.FailedPrecondition || code == codes.InvalidArgument:
		r.a.finish(ctx, r.claim, agentstore.Finish{State: agentstore.StateFailed, OutcomeCode: "rejected", OutcomeMessage: status.Convert(err).Message()})
	default:
		r.a.requeue(ctx, r.claim, err)
	}
}

func (a *Agentd) requeue(ctx context.Context, claim agentstore.Claim, cause error) {
	a.holdOff()
	if err := a.store.Requeue(ctx, claim.RunID, claim.LeaseToken); err != nil {
		a.log.Warn("agentd: requeue", "run_id", claim.RunID, "err", err)
		return
	}
	a.log.Info("agentd: run queued again", "run_id", claim.RunID, "cause", status.Code(cause).String())
}

func (a *Agentd) finish(ctx context.Context, claim agentstore.Claim, f agentstore.Finish) {
	f.DeliveriesOff = a.deliverer == nil
	if err := a.store.FinishRun(ctx, claim.AccountID, claim.RunID, claim.LeaseToken, f); err != nil {
		a.log.Warn("agentd: finish", "run_id", claim.RunID, "state", f.State, "err", err)
		return
	}
	a.log.Info("agentd: run finished", "run_id", claim.RunID, "state", f.State, "outcome", f.OutcomeCode)
}

// logTail keeps the last logTailBytes of a run's output.
type logTail struct {
	buf     []byte
	dropped bool
}

func (t *logTail) write(p []byte) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - logTailBytes; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.dropped = true
	}
}

// String is the tail as storable text: valid UTF-8 (the cut may split a
// character), no NUL bytes, and within the column's byte bound.
func (t *logTail) String() string {
	text := strings.ReplaceAll(strings.ToValidUTF8(string(t.buf), ""), "\x00", "")
	for len(text) > logTailBytes {
		_, size := utf8.DecodeRuneInString(text)
		text = text[size:]
	}
	return text
}
