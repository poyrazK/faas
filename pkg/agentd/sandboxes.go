package agentd

// Sandbox sessions (ADR-20261027090013 §1, §5). A replica claims a queued
// session under a lease and holds one schedd RunSandbox stream for the
// session's life: schedd keeps the reservation and vmmd the VM as long as
// the stream lasts. The replica owns the session's state and its timers:
// it freezes the VM after SandboxIdleFreezeSec without calls, and ends the
// session after its idle timeout, at its end, or on a close request. vmmd
// reports calls on the stream; their time reaches the database only with
// each lease renewal.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// SandboxStore is the sandbox_sessions surface agentd writes;
// *agentstore.Store implements it.
type SandboxStore interface {
	ClaimSandbox(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (agentstore.SandboxClaim, bool, error)
	MarkSandboxReady(ctx context.Context, id, token, nodeID, instanceID string) error
	RenewSandbox(ctx context.Context, id, token string, ttl time.Duration, lastActivity time.Time) (agentstore.SandboxLease, error)
	SetSandboxFrozen(ctx context.Context, id, token string, frozen bool) error
	RequeueSandbox(ctx context.Context, id, token string) error
	FinishSandbox(ctx context.Context, id, token, state, code, message string) error
	ExpireSandboxLeases(ctx context.Context) (requeued, failed, expired int64, err error)
}

// SandboxSchedd opens RunSandbox streams; scheddpb.ScheddClient implements
// it.
type SandboxSchedd interface {
	RunSandbox(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[scheddpb.SandboxRunRequest, scheddpb.SandboxRunEvent], error)
}

// SandboxConfig tunes a replica's sessions.
type SandboxConfig struct {
	// Concurrency bounds the sessions this replica holds at once; sessions
	// are long-lived, so they have slots of their own.
	Concurrency int
	// Tick is how often a session's timers are checked.
	Tick time.Duration
	// IdleFreeze is how long a session goes without calls before its VM is
	// frozen; SandboxIdleFreezeSec unless set.
	IdleFreeze time.Duration
}

func (c SandboxConfig) withDefaults() SandboxConfig {
	if c.Concurrency <= 0 {
		c.Concurrency = 64
	}
	if c.Tick <= 0 {
		c.Tick = time.Second
	}
	if c.IdleFreeze <= 0 {
		c.IdleFreeze = api.SandboxIdleFreezeSec * time.Second
	}
	return c
}

// Session outcomes.
const (
	sandboxOutcomeClosed    = "closed"
	sandboxOutcomeIdle      = "idle"
	sandboxOutcomeLifetime  = "lifetime"
	sandboxOutcomeRejected  = "rejected"
	sandboxOutcomeVMLost    = "vm_lost"
	sandboxOutcomeUnplanned = "plan_has_no_sessions"
)

// Sandboxes drives one replica's sandbox sessions.
type Sandboxes struct {
	store  SandboxStore
	schedd SandboxSchedd
	cfg    Config
	scfg   SandboxConfig
	log    *slog.Logger
	now    func() time.Time
}

// NewSandboxes returns a replica's session driver; cfg is the replica's
// lease and poll configuration.
func (a *Agentd) NewSandboxes(store SandboxStore, schedd SandboxSchedd, scfg SandboxConfig) *Sandboxes {
	return &Sandboxes{store: store, schedd: schedd, cfg: a.cfg, scfg: scfg.withDefaults(), log: a.log, now: time.Now}
}

// Run claims and holds sessions until ctx ends, and sweeps their expired
// leases.
func (s *Sandboxes) Run(ctx context.Context) error {
	go s.sweep(ctx)
	slots := make(chan struct{}, s.scfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		claim, found, err := s.claim(ctx)
		if err != nil || !found {
			<-slots
			if err != nil && ctx.Err() == nil {
				s.log.Warn("agentd: sandbox claim failed", "err", err)
			}
			if !sleep(ctx, s.cfg.PollInterval) {
				return nil
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			s.drive(ctx, claim)
		}()
	}
}

// ClaimOnce claims one session and holds it to its end. It reports whether
// a session was claimed.
func (s *Sandboxes) ClaimOnce(ctx context.Context) (bool, error) {
	claim, found, err := s.claim(ctx)
	if err != nil || !found {
		return false, err
	}
	s.drive(ctx, claim)
	return true, nil
}

func (s *Sandboxes) claim(ctx context.Context) (agentstore.SandboxClaim, bool, error) {
	return s.store.ClaimSandbox(ctx, s.cfg.Owner, uuid.NewString(), s.cfg.LeaseTTL, func(plan string) int {
		p := api.Plan(plan)
		if !p.Valid() {
			return 0
		}
		return api.AgentRunConcurrentPerAccount[p.PlanIndex()]
	})
}

// SweepOnce handles expired session leases once.
func (s *Sandboxes) SweepOnce(ctx context.Context) error {
	requeued, failed, expired, err := s.store.ExpireSandboxLeases(ctx)
	if requeued+failed+expired > 0 {
		s.log.Info("agentd: expired sandbox leases", "requeued", requeued, "failed", failed, "expired", expired)
	}
	return err
}

func (s *Sandboxes) sweep(ctx context.Context) {
	tick := time.NewTicker(s.cfg.SweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("agentd: sandbox sweep failed", "err", err)
			}
		}
	}
}

// session is one held session's state; the driving goroutine and the
// event receiver share it under mu.
type session struct {
	claim agentstore.SandboxClaim

	mu sync.Mutex
	// ready: the VM is up and its placement recorded.
	ready  bool
	frozen bool
	// freezing: a freeze was asked and has not been answered.
	freezing     bool
	inFlight     int
	lastActivity time.Time
	lease        agentstore.SandboxLease
	// end is the decided terminal state and outcome, once there is one.
	end *sessionEnd
}

type sessionEnd struct{ state, code, message string }

func (ss *session) decide(state, code, message string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.end != nil {
		return false
	}
	ss.end = &sessionEnd{state: state, code: code, message: message}
	return true
}

func (ss *session) decided() *sessionEnd {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.end
}

// drive holds one claimed session to its end.
func (s *Sandboxes) drive(ctx context.Context, claim agentstore.SandboxClaim) {
	plan := api.Plan(claim.Plan)
	if !plan.Valid() || api.SandboxMaxLifetimeSec[plan.PlanIndex()] == 0 {
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeUnplanned, "the account's plan has no sandbox sessions"})
		return
	}
	vcpu := api.AgentRunVCPUMax[plan.PlanIndex()]
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := s.schedd.RunSandbox(streamCtx)
	if err != nil {
		s.requeue(ctx, claim, err)
		return
	}
	var sendMu sync.Mutex
	send := func(req *scheddpb.SandboxRunRequest) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(req)
	}
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Start{Start: &scheddpb.SandboxRunStart{
		SandboxId: claim.ID, AccountId: claim.AccountID, Plan: claim.Plan, RamMb: int32(claim.RAMMB), Vcpu: int32(vcpu),
		CpuMillicores: int32(vcpu * 1000), NetworkMode: claim.Network,
	}}}); err != nil {
		s.requeue(ctx, claim, err)
		return
	}
	ss := &session{claim: claim, lease: claim.SandboxLease, lastActivity: s.now()}
	closeVM := func() {
		_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
	}
	lost := make(chan struct{})
	go s.keepSession(streamCtx, ss, closeVM, stop, lost)
	go s.timeSession(streamCtx, ss, send, closeVM)
	recvErr := s.receive(streamCtx, stream, ss, send)
	stop()
	select {
	case <-lost:
		// Another replica may hold the session now; the sweeper decides.
		return
	default:
	}
	if end := ss.decided(); end != nil {
		s.finish(ctx, claim, end)
		return
	}
	ss.mu.Lock()
	ready := ss.ready
	ss.mu.Unlock()
	switch code := status.Code(recvErr); {
	case ctx.Err() != nil:
		// The replica is stopping; the lease lapses and the sweeper decides.
	case ready:
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeVMLost, "the session's VM ended unexpectedly"})
	case code == codes.FailedPrecondition || code == codes.InvalidArgument:
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeRejected, "the session is outside the account's plan"})
	default:
		s.requeue(ctx, claim, recvErr)
	}
}

// receive handles schedd's events until the stream ends.
func (s *Sandboxes) receive(ctx context.Context, stream grpc.BidiStreamingClient[scheddpb.SandboxRunRequest, scheddpb.SandboxRunEvent],
	ss *session, send func(*scheddpb.SandboxRunRequest) error) error {
	var placed *scheddpb.AgentRunPlaced
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		switch {
		case ev.GetPlaced() != nil:
			placed = ev.GetPlaced()
			if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}}); err != nil {
				return err
			}
		case ev.GetBooted() != nil:
			if placed == nil {
				return status.Error(codes.Internal, "schedd booted a session it never placed")
			}
			if err := s.store.MarkSandboxReady(ctx, ss.claim.ID, ss.claim.LeaseToken, placed.GetNodeId(), placed.GetInstance()); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.ready, ss.lastActivity = true, s.now()
			ss.mu.Unlock()
			s.log.Info("agentd: sandbox ready", "sandbox_id", ss.claim.ID, "node", placed.GetNodeId(),
				"method", ev.GetBooted().GetMethod(), "boot_ms", ev.GetBooted().GetBootMs())
		case ev.GetActivity() != nil:
			a := ev.GetActivity()
			ss.mu.Lock()
			ss.inFlight = int(a.GetCallsInFlight())
			if ended := time.UnixMilli(a.GetLastCallEndedUnixMs()); a.GetLastCallEndedUnixMs() > 0 && ended.After(ss.lastActivity) {
				ss.lastActivity = ended
			}
			if ss.inFlight > 0 {
				ss.lastActivity = s.now()
			}
			ss.mu.Unlock()
		case ev.GetFrozen() != nil:
			if err := s.store.SetSandboxFrozen(ctx, ss.claim.ID, ss.claim.LeaseToken, true); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.frozen, ss.freezing = true, false
			ss.mu.Unlock()
		case ev.GetFreezeRefused() != nil:
			ss.mu.Lock()
			ss.freezing = false
			// A refused freeze waits out another idle period.
			ss.lastActivity = s.now()
			ss.mu.Unlock()
		case ev.GetThawed() != nil:
			if err := s.store.SetSandboxFrozen(ctx, ss.claim.ID, ss.claim.LeaseToken, false); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.frozen, ss.lastActivity = false, s.now()
			ss.mu.Unlock()
		}
	}
}

// keepSession renews the lease every third of its TTL with the last call
// seen, and closes the session when apid asked. A lost lease ends the
// stream, which destroys the VM.
func (s *Sandboxes) keepSession(ctx context.Context, ss *session, closeVM, stop func(), lost chan<- struct{}) {
	tick := time.NewTicker(s.cfg.LeaseTTL / 3)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ss.mu.Lock()
		last := ss.lastActivity
		ss.mu.Unlock()
		lease, err := s.store.RenewSandbox(ctx, ss.claim.ID, ss.claim.LeaseToken, s.cfg.LeaseTTL, last)
		if errors.Is(err, agentstore.ErrLeaseLost) {
			close(lost)
			stop()
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("agentd: sandbox lease renewal failed", "sandbox_id", ss.claim.ID, "err", err)
			}
			continue
		}
		ss.mu.Lock()
		ss.lease = lease
		ss.mu.Unlock()
		if lease.CloseRequested && ss.decide(agentstore.SandboxClosed, sandboxOutcomeClosed, "") {
			closeVM()
		}
	}
}

// timeSession checks the session's timers every tick: its end, its idle
// timeout and the idle freeze.
func (s *Sandboxes) timeSession(ctx context.Context, ss *session, send func(*scheddpb.SandboxRunRequest) error, closeVM func()) {
	tick := time.NewTicker(s.scfg.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := s.now()
		ss.mu.Lock()
		idle := now.Sub(ss.lastActivity)
		if ss.inFlight > 0 {
			idle = 0
		}
		ready, frozen, freezing, lease := ss.ready, ss.frozen, ss.freezing, ss.lease
		freeze := ready && !frozen && !freezing && idle >= s.scfg.IdleFreeze
		if freeze {
			ss.freezing = true
		}
		ss.mu.Unlock()
		switch {
		case !now.Before(lease.ExpiresAt):
			if ss.decide(agentstore.SandboxExpired, sandboxOutcomeLifetime, "the session reached its lifetime") {
				closeVM()
			}
		case ready && lease.IdleTimeout > 0 && idle >= lease.IdleTimeout:
			if ss.decide(agentstore.SandboxExpired, sandboxOutcomeIdle, "the session had no calls for its idle timeout") {
				closeVM()
			}
		case freeze:
			_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Freeze{Freeze: &scheddpb.SandboxRunFreeze{}}})
		}
	}
}

func (s *Sandboxes) finish(ctx context.Context, claim agentstore.SandboxClaim, end *sessionEnd) {
	if err := s.store.FinishSandbox(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken, end.state, end.code, end.message); err != nil {
		s.log.Warn("agentd: finish sandbox failed", "sandbox_id", claim.ID, "err", err)
		return
	}
	s.log.Info("agentd: sandbox ended", "sandbox_id", claim.ID, "state", end.state, "outcome", end.code)
}

// requeue returns a session that never booted, after a capacity refusal or
// a transport failure before placement.
func (s *Sandboxes) requeue(ctx context.Context, claim agentstore.SandboxClaim, cause error) {
	if err := s.store.RequeueSandbox(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken); err != nil {
		s.log.Warn("agentd: requeue sandbox failed", "sandbox_id", claim.ID, "err", err)
		return
	}
	s.log.Info("agentd: sandbox requeued", "sandbox_id", claim.ID, "cause", status.Code(cause).String())
}
