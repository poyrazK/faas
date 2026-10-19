// adr: 566 — vmmd owns local execution deadlines and retirement evidence.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

type FinancialComputeAuthority interface {
	CancelFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	FinancialComputeRetired(context.Context, string, string) (bool, error)
	FinancialComputeLease(context.Context, string, string) (financial.ComputeLease, bool, error)
	AcquireFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	RenewFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	SettleFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
}

type localComputeLease struct {
	egressObserved uint64
	egressApplied  int64
	egressBroken   bool
	mu             sync.Mutex
	lease          financial.ComputeLease
	request        financial.ComputeRequest
	deadline       time.Time // derived from a monotonic request-start time
	// pendingRenew is a renewal whose outcome is unknown. It is replayed with
	// the same request identity, so a lost committed reply is adopted.
	pendingRenew   *financial.ComputeRequest
	pendingStarted time.Time
	// cleaned marks an unbudgeted lease whose physical cleanup completed but
	// whose settlement failed; recovery or the next wake settles it.
	cleaned    bool
	cancelBoot context.CancelFunc
	done       chan struct{}
	once       sync.Once
}

// Configure only after native recovery and durable node registration. The
// authority also records unbudgeted boots so activation can join all producers.
func (m *Manager) WithFinancialComputeAuthority(a FinancialComputeAuthority, node, incarnation string) *Manager {
	m.computeAuthority, m.computeNode, m.computeIncarnation = a, node, incarnation
	return m
}

func (m *Manager) beginFinancialCompute(ctx context.Context, req WakeRequest, flight *instanceFlight) (*localComputeLease, error) {
	if m.computeAuthority == nil || req.AppID == "" || req.ExportDir != "" {
		return nil, nil
	}
	mbs := make([]int, 0, len(req.Sidecars))
	for _, s := range req.Sidecars {
		mb := s.RamMB
		if mb == 0 {
			mb = req.MemSizeMiB
		}
		mbs = append(mbs, mb)
	}
	fields, _ := wire.FromContext(ctx)
	attempt := fields.WakeID
	fenced := uuid.Validate(attempt) == nil
	if !fenced {
		// Without a persisted wake ID there is no replay identity to derive.
		// The ledger only grants such an attempt when no strict cap applies,
		// and a fresh ID keeps a later wake of a reused instance unfenced.
		attempt = uuid.NewString()
	}
	r := financial.ComputeRequest{AttemptID: attempt, Fenced: fenced, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: req.AccountID, AppID: req.AppID, InstanceID: req.Instance, NodeID: m.computeNode, Incarnation: m.computeIncarnation, BillableMB: api.BillableRAMMBWithSidecars(req.MemSizeMiB, mbs)}
	return m.beginComputeRequest(ctx, r, flight)
}

func (m *Manager) beginComputeRequest(ctx context.Context, r financial.ComputeRequest, flight *instanceFlight) (*localComputeLease, error) {
	var bindErr error
	r, bindErr = financial.BindComputeRequest(r)
	if bindErr != nil {
		return nil, bindErr
	}
	local := &localComputeLease{request: r, cancelBoot: flight.cancel, done: make(chan struct{})}
	m.mu.Lock()
	if m.computeLeases == nil {
		m.computeLeases = map[string]*localComputeLease{}
	}
	if previous := m.computeLeases[r.InstanceID]; previous != nil {
		m.mu.Unlock()
		if !previous.isCleaned() || m.settleFinancialCompute(ctx, r.InstanceID) != nil {
			return nil, financial.ErrComputeUnavailable
		}
		m.mu.Lock()
		if m.computeLeases[r.InstanceID] != nil {
			m.mu.Unlock()
			return nil, financial.ErrComputeUnavailable
		}
	}
	m.computeLeases[r.InstanceID] = local
	m.mu.Unlock()
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, api.FinancialComputeAuthorityTimeout)
	l, err := m.computeAuthority.AcquireFinancialCompute(callCtx, r)
	cancel()
	if err != nil {
		if errors.Is(err, financial.ErrComputeBudget) || errors.Is(err, financial.ErrComputeFence) {
			m.mu.Lock()
			if m.computeLeases[r.InstanceID] == local {
				delete(m.computeLeases, r.InstanceID)
			}
			m.mu.Unlock()
			return nil, fmt.Errorf("compute reservation: %w", err)
		}
		return local, fmt.Errorf("compute reservation: %w", err)
	}
	local.mu.Lock()
	local.lease = l
	if l.EgressFunded && !financialEgressClockFits(l, started) {
		local.egressBroken = true
		local.mu.Unlock()
		return local, financial.ErrComputeUnavailable
	}
	local.deadline = started.Add(l.RunUntil.Sub(l.GrantedAt))
	local.mu.Unlock()
	if l.Strict {
		if !time.Now().Before(local.deadline) {
			return local, financial.ErrComputeBudget
		}
		// VM authority outlives the RPC; retain tracing without its cancellation.
		go m.watchFinancialCompute(context.WithoutCancel(ctx), local)
	}
	return local, nil
}

func (l *localComputeLease) egressFunded() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lease.EgressFunded
}

// applyFinancialEgress installs the funded public-byte budget on a new
// namespace and records the grant sequence it reflects.
func (l *localComputeLease) applyFinancialEgress(nc *netns.Config) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lease.EgressFunded {
		nc.EgressBudgetBytes = l.lease.EgressGrant
		nc.EgressBudgetUntil = l.lease.RunUntil.Add(-2 * api.FinancialComputeAuthorityTimeout)
		l.egressApplied = l.lease.EgressSequence
	}
}

func computeRequest(l financial.ComputeLease) financial.ComputeRequest {
	return financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), Revision: l.Revision, AccountID: l.AccountID, AppID: l.AppID, JobID: l.JobID, JobRunID: l.JobRunID, JobTaskIndex: l.JobTaskIndex, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB}
}

func (m *Manager) watchFinancialCompute(ctx context.Context, local *localComputeLease) {
	interval := api.FinancialComputeRenewInterval
	local.mu.Lock()
	if local.lease.EgressFunded {
		interval = api.FinancialEgressPollInterval
	}
	local.mu.Unlock()
	// Ticks drift against a period equal to the renewal interval; half a
	// tick of slack keeps a 20 s renewal from skipping every other tick.
	renewEvery := api.FinancialComputeRenewInterval - interval/2
	nextRenew := time.Now().Add(renewEvery)
	var retryAt time.Time
	tick := time.NewTicker(interval)
	defer tick.Stop()
	local.mu.Lock()
	deadline := local.deadline
	instance := local.lease.InstanceID
	local.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	renew := true
	for {
		select {
		case <-local.done:
			return
		case <-tick.C:
			if !renew {
				continue
			}
			refill, err := m.financialEgressLow(local)
			if err != nil {
				renew = false
				continue
			}
			now := time.Now()
			if now.Before(retryAt) || !refill && now.Before(nextRenew) {
				continue
			}
			renewed, stop := m.renewFinancialComputeCredit(ctx, local, refill)
			if stop {
				renew = false
			}
			if !renewed {
				// A transient failure keeps the last deadline; retry
				// before it, without polling the database every tick.
				retryAt = time.Now().Add(api.FinancialComputeAuthorityTimeout)
				continue
			}
			nextRenew = time.Now().Add(renewEvery)
			local.mu.Lock()
			deadline = local.deadline
			local.mu.Unlock()
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(deadline))
		case <-timer.C:
			// No scheduler, gateway, meterd or database round trip is needed
			// to revoke an in-flight boot or start local physical retirement.
			if local.cancelBoot != nil {
				local.cancelBoot()
			}
			for {
				retireCtx, cancel := context.WithTimeout(ctx, api.FinancialComputeTeardownReserve)
				err := m.retireExpiredFinancialCompute(retireCtx, instance, local)
				cancel()
				if err == nil {
					return
				}
				m.log.Warn("financial compute retirement pending", "instance", instance)
				select {
				case <-local.done:
					return
				case <-tick.C:
				}
			}
		}
	}
}

// renewFinancialComputeCredit reports whether the local deadline moved, and
// whether renewal must stop for this lease. Denial, fencing and broken egress
// accounting are final; a failed or lost RPC is retried by the caller.
func (m *Manager) renewFinancialComputeCredit(ctx context.Context, local *localComputeLease, refill bool) (renewed, stop bool) {
	local.mu.Lock()
	defer local.mu.Unlock()
	select {
	case <-local.done:
		return false, true
	default:
	}
	if local.egressBroken || !time.Now().Add(api.FinancialComputeRenewMargin).Before(local.deadline) {
		return false, true
	}
	r := computeRequest(local.lease)
	r.EgressRefill = refill
	started := time.Now()
	if p := local.pendingRenew; p != nil {
		// Replay the exact request: a fresh identity against a revision the
		// lost reply already advanced would be fenced.
		r, started = *p, local.pendingStarted
	}
	callCtx, cancel := context.WithTimeout(ctx, api.FinancialComputeAuthorityTimeout)
	defer cancel()
	l, err := m.computeAuthority.RenewFinancialCompute(callCtx, r)
	if err != nil {
		if errors.Is(err, financial.ErrComputeBudget) || errors.Is(err, financial.ErrComputeFence) {
			local.pendingRenew = nil
			return false, true
		}
		local.pendingRenew, local.pendingStarted = &r, started
		return false, false
	}
	local.pendingRenew = nil
	old := local.lease
	// Settlement needs the committed revision, even when the reply is late
	// or the network helper outcome is uncertain.
	local.lease = l
	// A late DB commit may fund more time, but cannot revive local execution.
	if !time.Now().Before(local.deadline) {
		return false, true
	}
	if l.EgressFunded {
		if !financialEgressClockFits(l, started) {
			local.egressBroken = true
			return false, true
		}
		if !m.syncFinancialEgress(ctx, local, l, r.EgressRefill && l.EgressSequence > old.EgressSequence) {
			return false, true
		}
	}
	local.deadline = started.Add(l.RunUntil.Sub(l.GrantedAt))
	return true, false
}

// Pin both attempt identity and destruction under Manager.mu. A delayed timer
// for a settled lease cannot stop a replacement reusing its instance ID.
func (m *Manager) retireExpiredFinancialCompute(ctx context.Context, instance string, expected *localComputeLease) error {
	m.mu.Lock()
	select {
	case <-expected.done:
		m.mu.Unlock()
		return nil
	default:
	}
	if m.computeLeases[instance] != expected {
		m.mu.Unlock()
		return nil
	}
	if previous := m.instanceStops[instance]; previous != nil {
		m.mu.Unlock()
		select {
		case <-previous.done:
			return previous.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	stop := &instanceTeardownFlight{done: make(chan struct{})}
	if m.instanceStops == nil {
		m.instanceStops = map[string]*instanceStop{}
	}
	m.instanceStops[instance] = stop
	m.mu.Unlock()
	code, err := m.destroyWithReservation(ctx, context.WithoutCancel(ctx), instance, "")
	if errors.Is(err, os.ErrNotExist) && m.financialComputeNeverLaunched(ctx, instance) {
		err = nil
	}
	if err == nil {
		err = m.settleFinancialCompute(ctx, instance)
	}
	m.finishInstanceTeardown(instance, stop, code, false, false, err)
	return err
}

func (m *Manager) financialComputeNeverLaunched(ctx context.Context, instance string) bool {
	v := m.nativeVMM()
	if v == nil || m.HasInstanceOwnership(instance) {
		return false
	}
	if _, err := v.nativeRecoveryRuntime().journal.read(instance); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Repeat the native process/resource inventory, rather than treating a
	// missing local journal as physical absence. The destruction join keeps
	// this instance's original producer and all replacements closed.
	leases, err := v.nativeRecoveryLeases(ctx)
	if err != nil {
		return false
	}
	for _, l := range leases {
		if l.Instance == instance {
			return false
		}
	}
	m.alloc.mu.Lock()
	_, allocated := m.alloc.byInstance[instance]
	m.alloc.mu.Unlock()
	return !allocated
}

// Only an authoritative inventory read plus complete local unwind can discard
// an acquire whose response was lost. A late committed row stays funded and is
// physically reconciled on the next pass.
func (m *Manager) ForgetUnfundedFinancialComputeAttempts(rows []financial.ComputeLease) {
	seen := map[string]bool{}
	for _, l := range rows {
		seen[l.ID] = true
	}
	m.mu.Lock()
	candidates := map[string]*localComputeLease{}
	for id, l := range m.computeLeases {
		if m.instanceFlights[id] == nil && m.pendingCleanup[id] == nil && m.live[id] == nil && m.qualificationInstances[id] == nil {
			candidates[id] = l
		}
	}
	m.mu.Unlock()
	for instance, l := range candidates {
		l.mu.Lock()
		if l.lease.Revision == 0 && !seen[l.request.LeaseID] {
			m.mu.Lock()
			if m.computeLeases[instance] == l && m.instanceFlights[instance] == nil && m.pendingCleanup[instance] == nil && m.live[instance] == nil && m.qualificationInstances[instance] == nil {
				delete(m.computeLeases, instance)
				l.once.Do(func() { close(l.done) })
			}
			m.mu.Unlock()
		}
		l.mu.Unlock()
	}
}

// Called only after the native owner confirmed absence, before the allocator
// can reuse an identity. A failed DB settlement retains the local receipt and
// monetary reservation; cleanup can retry the exact fenced attempt.
func (m *Manager) settleFinancialCompute(ctx context.Context, instance string) error {
	if m.computeAuthority == nil {
		return nil
	}
	m.mu.Lock()
	local := m.computeLeases[instance]
	m.mu.Unlock()
	if local == nil {
		return nil
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	if local.lease.Revision == 0 {
		return financial.ErrComputeUnavailable
	}
	if local.lease.SettledAt != nil {
		return nil
	}
	r := computeRequest(local.lease)
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.FinancialComputeAuthorityTimeout)
	defer cancel()
	l, err := m.computeAuthority.SettleFinancialCompute(callCtx, r)
	if errors.Is(err, financial.ErrComputeFence) {
		l, err = m.settleRefreshedFinancialCompute(callCtx, local)
	}
	if err != nil {
		return err
	}
	local.pendingRenew = nil
	local.lease = l
	local.once.Do(func() { close(local.done) })
	m.mu.Lock()
	if m.computeLeases[instance] == local {
		delete(m.computeLeases, instance)
	}
	m.mu.Unlock()
	return nil
}

// A renewal whose reply was lost may have advanced the durable revision.
// Physical absence is already confirmed and local.mu excludes this lease's
// renewals, so settle the same receipt at its current revision.
func (m *Manager) settleRefreshedFinancialCompute(ctx context.Context, local *localComputeLease) (financial.ComputeLease, error) {
	old := local.lease
	current, found, err := m.computeAuthority.FinancialComputeLease(ctx, old.AccountID, old.ID)
	if err != nil {
		return financial.ComputeLease{}, err
	}
	if !found || current.ID != old.ID || current.Fence != old.Fence || current.NodeID != old.NodeID || current.Incarnation != old.Incarnation || current.InstanceID != old.InstanceID || current.Revision < old.Revision {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	if current.SettledAt != nil {
		return current, nil
	}
	if current.Revision == old.Revision {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	local.lease = current
	return m.computeAuthority.SettleFinancialCompute(ctx, computeRequest(current))
}

func (l *localComputeLease) isCleaned() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cleaned
}

// settleCleanedFinancialCompute settles after confirmed physical cleanup. An
// unbudgeted lease funds nothing, so a failed settlement must not hold the
// slot; its local receipt stays for the recovery loop or the next wake. A
// strict or uncertain lease still retains capacity until settlement.
func (m *Manager) settleCleanedFinancialCompute(ctx context.Context, instance string) error {
	err := m.settleFinancialCompute(ctx, instance)
	if err == nil {
		return nil
	}
	m.mu.Lock()
	local := m.computeLeases[instance]
	m.mu.Unlock()
	if local == nil {
		return err
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	if local.lease.Revision == 0 || local.lease.Strict || local.lease.SettledAt != nil {
		return err
	}
	local.cleaned = true
	m.log.Warn("financial compute settlement deferred for unbudgeted instance", "instance", instance, "err", err)
	return nil
}

func (m *Manager) finishFinancialComputeBoot(ctx context.Context, instance string, local *localComputeLease, failed bool) {
	if local == nil || !failed {
		return
	}
	m.mu.Lock()
	absent := m.live[instance] == nil && m.qualificationInstances[instance] == nil && m.pendingCleanup[instance] == nil && m.instanceFlights[instance] == nil
	m.mu.Unlock()
	if absent {
		_ = m.settleFinancialCompute(ctx, instance)
	}
}

// Startup/periodic recovery first joins the physical owner. It never reclaims
// money just because a node incarnation or execution deadline expired.
func (m *Manager) RecoverFinancialComputeLease(ctx context.Context, l financial.ComputeLease) error {
	if m.computeAuthority == nil || l.NodeID != m.computeNode || l.SettledAt != nil {
		return financial.ErrComputeFence
	}
	m.computeRecoveryMu.Lock()
	defer m.computeRecoveryMu.Unlock()
	m.mu.Lock()
	local := m.computeLeases[l.InstanceID]
	flight := m.instanceFlights[l.InstanceID]
	pending := m.pendingCleanup[l.InstanceID] != nil
	live := m.live[l.InstanceID] != nil
	m.mu.Unlock()
	if local != nil {
		local.mu.Lock()
		// An acquire whose reply was lost has no lease yet; its bound
		// request still names the only durable receipt it may adopt.
		id := local.lease.ID
		if id == "" {
			id = local.request.LeaseID
		}
		if id != l.ID {
			local.mu.Unlock()
			return financial.ErrComputeFence
		}
		if local.cleaned && flight == nil && !pending && !live {
			local.mu.Unlock()
			return m.settleFinancialCompute(ctx, l.InstanceID)
		}
		if flight != nil || (!pending && live && (!l.Strict || time.Now().Before(local.deadline))) {
			local.mu.Unlock()
			return nil
		}
		if l.Revision > local.lease.Revision {
			local.lease = l
		}
		local.mu.Unlock()
	} else {
		if flight != nil {
			return nil
		}
		// The node inventory may have raced the previous owner's final
		// settlement. Refresh this exact receipt, not the instance's latest
		// attempt, before reconstructing allocator or teardown ownership.
		callCtx, cancel := context.WithTimeout(ctx, api.FinancialComputeAuthorityTimeout)
		current, found, err := m.computeAuthority.FinancialComputeLease(callCtx, l.AccountID, l.ID)
		cancel()
		if err != nil {
			return err
		}
		if !found || current.SettledAt != nil || current.ID != l.ID || current.Fence != l.Fence || current.NodeID != l.NodeID || current.InstanceID != l.InstanceID {
			return financial.ErrComputeFence
		}
		l = current
		if err := m.retainUnsettledFinancialComputeSlot(ctx, l.InstanceID); err != nil {
			return err
		}
		local = &localComputeLease{lease: l, request: computeRequest(l), done: make(chan struct{})}
		m.mu.Lock()
		if m.computeLeases == nil {
			m.computeLeases = map[string]*localComputeLease{}
		}
		if m.computeLeases[l.InstanceID] != nil {
			m.mu.Unlock()
			return nil
		}
		m.computeLeases[l.InstanceID] = local
		m.mu.Unlock()
	}
	return m.retireExpiredFinancialCompute(ctx, l.InstanceID, local)
}

// Physical cleanup can commit before financial settlement. After a crash the
// native inventory correctly omits that cleared slot, but a failed settlement
// must not let another VM reuse it while retirement is still retryable. Join
// the durable native receipt before any cleanup effects; a slot already held
// by another instance fails closed without touching that replacement.
func (m *Manager) retainUnsettledFinancialComputeSlot(ctx context.Context, instance string) error {
	v := m.nativeVMM()
	if v == nil {
		return nil
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return err
	}
	record, err := v.nativeRecoveryRuntime().journal.read(instance)
	if errors.Is(err, os.ErrNotExist) {
		return nil // An unstarted/denied attempt owns no allocator identity.
	}
	if err != nil {
		return err
	}
	if !record.ResourcesRemoved {
		return nil // Native recovery already quarantined physical ownership.
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.nativeRecovered[instance]) > 0 {
		return nil
	}
	m.alloc.mu.Lock()
	_, allocated := m.alloc.byInstance[instance]
	m.alloc.mu.Unlock()
	if allocated {
		return nil // A local failed cleanup still retains its original slot.
	}
	if err := m.alloc.reserveRecovered([]Lease{record.Lease}); err != nil {
		return err
	}
	if m.nativeRecovered == nil {
		m.nativeRecovered = make(map[string][]Lease)
	}
	m.nativeRecovered[instance] = []Lease{record.Lease}
	return nil
}

// Bypass paths without ordinary Wake funding must be refused for strict apps.
func (m *Manager) WithFinancialComputeBypassGuard(guard func(context.Context, string) error) *Manager {
	m.computeBypassGuard = guard
	return m
}
func (m *Manager) checkFinancialComputeBypass(ctx context.Context, app string) error {
	if m.computeBypassGuard == nil {
		return nil
	}
	if app == "" {
		return financial.ErrComputeUnavailable
	}
	return m.computeBypassGuard(ctx, app)
}

// The caller retains the instance destruction join throughout this method.
// Closing the durable attempt before acknowledging absence fences late RPCs.
func (m *Manager) cancelUnstartedFinancialCompute(ctx context.Context, instance string) bool {
	fields, _ := wire.FromContext(ctx)
	m.mu.Lock()
	local := m.computeLeases[instance]
	m.mu.Unlock()
	if local != nil {
		local.mu.Lock()
		if fields.AppID == "" {
			fields.AppID = local.lease.AppID
			if fields.AppID == "" {
				fields.AppID = local.request.AppID
			}
		}
		if fields.WakeID == "" {
			fields.WakeID = local.lease.AttemptID
			if fields.WakeID == "" {
				fields.WakeID = local.request.AttemptID
			}
		}
		local.mu.Unlock()
	}
	l, err := m.computeAuthority.CancelFinancialCompute(ctx, financial.ComputeRequest{NodeID: m.computeNode, Incarnation: m.computeIncarnation, AppID: fields.AppID, InstanceID: instance, AttemptID: fields.WakeID, Fenced: true})
	if err != nil || l.SettledAt == nil || l.InstanceID != instance || l.NodeID != m.computeNode || l.JobID == "" && (l.AppID != fields.AppID || fields.WakeID != "" && l.AttemptID != fields.WakeID) {
		return false
	}
	if local != nil {
		local.mu.Lock()
		if local.lease.ID == l.ID || local.lease.ID == "" && local.request.LeaseID == l.ID {
			local.lease = l
			local.once.Do(func() { close(local.done) })
			m.mu.Lock()
			if m.computeLeases[instance] == local {
				delete(m.computeLeases, instance)
			}
			m.mu.Unlock()
		}
		local.mu.Unlock()
	}
	return true
}
