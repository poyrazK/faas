package fcvm

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// The live Instance owns this ledger. Old process generations and late
// completions can never mutate a replacement's ledger, even with the same ID.
type functionExecutionState struct {
	owner     uint64
	highWater uint64
	active    map[uint64]time.Time
	retired   bool
	failed    bool
}

// WithFunctionExecutionActivity wires execution occupancy into the existing
// Stats, reaper, freeze and quiet-pause path without writing instances rows.
func (m *Manager) WithFunctionExecutionActivity(a *activity.ActivityTracker) *Manager {
	m.functionActivity = a
	return m
}

func functionExecutionBusy(inst *Instance) bool {
	return inst != nil && inst.functionExecutions != nil && (len(inst.functionExecutions.active) > 0 || inst.functionExecutions.failed)
}

// Startup health requests can execute before publication in live. Their
// ownership moves into the published Instance without a zero-count window.
// A fallback calls this only after Kill confirmed the failed restore exited.
func (m *Manager) beginFunctionExecutionBoot(lease Lease, req WakeRequest) {
	if m.functionActivity == nil || (req.Runtime != "node22" && req.Runtime != "node24") || req.ExecutionOnly || req.AppTaskOnly || lease.IsBuilder {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.functionStarting == nil {
		m.functionStarting = make(map[string]*Instance)
	}
	if previous := m.functionStarting[lease.Instance]; previous != nil {
		m.functionActivity.Forget(lease.Instance)
	}
	m.functionStarting[lease.Instance] = &Instance{Lease: lease, Runtime: req.Runtime, Plan: lease.Plan}
}

// dropFunctionStartingLocked removes the boot entry of a process generation
// that can no longer publish: it exited before registration, or another
// generation published. Its executions ended with that process. A retained
// entry would fence migration destination admission and let the watchdog
// cancel whichever flight is current for the instance.
func (m *Manager) dropFunctionStartingLocked(instance string, generation uint64) {
	starting := m.functionStarting[instance]
	if starting == nil || starting.Lease.processGeneration != generation {
		return
	}
	delete(m.functionStarting, instance)
	if m.functionActivity != nil {
		m.functionActivity.Forget(instance)
	}
}

// RegisterFunctionExecutionReceiver binds authority to the complete lease,
// including processGeneration, rather than a caller-supplied instance ID.
func (m *Manager) RegisterFunctionExecutionReceiver(v *JailerVMM) error {
	if m.functionActivity == nil {
		return errors.New("function lifetime: activity tracker is required")
	}
	if err := v.registerGuestVsockLeaseHandler(functionlifetime.HostPort, m.serveFunctionExecution); err != nil {
		return err
	}
	v.functionPauseBarrier = m.functionPauseBarrier
	return nil
}

func (m *Manager) serveFunctionExecution(lease Lease, conn net.Conn) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(functionlifetime.RPCTimeout)); err != nil {
		return "read", err
	}
	req, err := functionlifetime.ReadRequest(conn)
	if err != nil {
		return "protocol", err
	}
	reply := m.receiveFunctionExecution(lease, req)
	if err := functionlifetime.Write(conn, reply); err != nil {
		return "write", err
	}
	return "", nil
}

func (m *Manager) receiveFunctionExecution(lease Lease, req functionlifetime.Request) functionlifetime.Reply {
	if req.Kind != functionlifetime.Begin || req.Hook != "" {
		return m.applyFunctionExecution(lease, req, time.Now())
	}
	m.mu.Lock()
	booting := m.live[lease.Instance] == nil && m.functionStarting[lease.Instance] != nil
	m.mu.Unlock()
	if booting {
		return m.applyFunctionExecution(lease, req, time.Now())
	}
	ctx, cancel := context.WithTimeout(context.Background(), functionlifetime.RPCTimeout)
	defer cancel()
	ctx, inst, flight, err := m.registerLiveInstanceFlight(ctx, lease.Instance)
	if err != nil {
		return functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Busy}
	}
	defer m.finishInstanceFlight(lease.Instance, flight)
	if inst.Lease.processGeneration != lease.processGeneration || inst.Lease.Slot != lease.Slot {
		return functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Stale}
	}
	m.mu.Lock()
	flight.functionExecutionAdmission = true
	m.mu.Unlock()
	reply := m.applyFunctionExecution(lease, req, time.Now())
	if reply.Status == functionlifetime.OK {
		// The reservation precedes the quiet-pause check; ACK follows the
		// incarnation-pinned resume, so a racing pause cannot suspend work
		// after the handler has been allowed to start.
		if err := m.QuietResume(ctx, lease.Instance); err != nil {
			reply.Status = functionlifetime.Unavailable
		}
	}
	return reply
}

func (m *Manager) applyFunctionExecution(lease Lease, req functionlifetime.Request, now time.Time) functionlifetime.Reply {
	reply := functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Stale}
	if req.Validate() != nil || req.Owner == 0 {
		return reply
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[lease.Instance]
	booting := inst == nil
	if booting {
		inst = m.functionStarting[lease.Instance]
	}
	if inst == nil || inst.Lease.processGeneration != lease.processGeneration || inst.Lease.Slot != lease.Slot || m.instanceStops[lease.Instance] != nil || m.pendingCleanup[lease.Instance] != nil || m.functionActivity == nil {
		return reply
	}
	if booting && (m.instanceFlights[lease.Instance] == nil || m.instanceFlights[lease.Instance].cancelled) {
		return reply
	}
	if !inst.Lease.Plan.Valid() || (inst.Runtime != "node22" && inst.Runtime != "node24") || inst.QualificationOnly || inst.ExecutionOnly || inst.AppTaskOnly || inst.IsJob {
		reply.Status = functionlifetime.Unavailable
		return reply
	}
	reply.Plan = inst.Lease.Plan
	s := inst.functionExecutions
	if s == nil {
		s = &functionExecutionState{owner: req.Owner, active: make(map[uint64]time.Time)}
		inst.functionExecutions = s
	}
	if req.Owner < s.owner {
		return reply
	}
	if req.Owner > s.owner {
		if len(s.active) > 0 || s.failed {
			// A runner restart cannot acknowledge orphaned interpreters.
			// Seal admission; the watchdog retires this physical VM.
			s.failed = true
			return reply
		}
		if req.Kind != functionlifetime.Begin {
			return reply
		}
		s.owner, s.highWater, s.retired = req.Owner, 0, false
	}
	switch req.Kind {
	case functionlifetime.Finish:
		// Tombstone unknown reservations as well: a cancelled Begin may
		// arrive after Finish on a different transport connection.
		s.highWater = max(s.highWater, req.Sequence)
		if _, held := s.active[req.Sequence]; held {
			delete(s.active, req.Sequence)
			m.functionActivity.EndExecution(lease.Instance)
		}
		reply.Status = functionlifetime.OK
	case functionlifetime.Retire:
		s.retired = true
		s.failed = s.failed || len(s.active) > 0
		reply.Status = functionlifetime.OK
	case functionlifetime.Begin:
		if s.retired || s.failed {
			return reply
		}
		if _, duplicate := s.active[req.Sequence]; duplicate {
			reply.Status = functionlifetime.OK
			return reply
		}
		if req.Sequence <= s.highWater {
			return reply
		}
		flight := m.instanceFlights[lease.Instance]
		blocked := !booting && flight != nil && !flight.functionExecutionAdmission
		if req.Hook == "before_checkpoint" {
			blocked = flight == nil || !flight.functionHookAdmission
		}
		if inst.Frozen || inst.Paused || blocked || functionMigrationActive(inst) && req.Hook != "before_checkpoint" {
			reply.Status = functionlifetime.Busy
			return reply
		}
		if len(s.active) >= api.FunctionInterpreterMaxWorkers*api.MustLimitsFor(inst.Lease.Plan).ConcurrencyPerVMBound {
			reply.Status = functionlifetime.Limit
			return reply
		}
		s.highWater = req.Sequence
		s.active[req.Sequence] = now.Add(api.FunctionExecutionLeaseDuration(inst.Lease.Plan, time.Duration(req.BudgetMS)*time.Millisecond))
		m.functionActivity.BeginExecution(lease.Instance)
		reply.Status = functionlifetime.OK
	}
	return reply
}

// Called at the final pause boundary, after optional customer hooks. Closing
// hook admission and observing zero executions happen under the same lock.
func (m *Manager) functionPauseBarrier(instance string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if flight := m.instanceFlights[instance]; flight != nil {
		flight.functionHookAdmission = false
	}
	inst := m.live[instance]
	if inst == nil {
		inst = m.functionStarting[instance]
	}
	if functionExecutionBusy(inst) {
		return ErrInstanceBusy
	}
	return nil
}

// RunFunctionExecutionWatchdog never releases an expired count. It retires the
// original VM through the existing incarnation-checked teardown reservation.
// An uncertain cleanup retains the allocator/native recovery ownership.
func (m *Manager) RunFunctionExecutionWatchdog(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.functionExecutionWatchdogPass(ctx, now)
		}
	}
}

func (m *Manager) functionExecutionWatchdogPass(ctx context.Context, now time.Time) {
	m.mu.Lock()
	var expired []*Instance
	var cancelBoots []context.CancelFunc
	for id, inst := range m.functionStarting {
		s := inst.functionExecutions
		if s == nil {
			continue
		}
		for _, deadline := range s.active {
			if !now.Before(deadline) {
				s.failed = true
				break
			}
		}
		if s.failed {
			if flight := m.instanceFlights[id]; flight != nil {
				cancelBoots = append(cancelBoots, flight.cancel)
			}
		}
	}
	for _, inst := range m.live {
		s := inst.functionExecutions
		if s == nil {
			continue
		}
		for _, deadline := range s.active {
			if !now.Before(deadline) {
				s.failed = true
				break
			}
		}
		if s.failed {
			expired = append(expired, inst)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancelBoots {
		cancel()
	}
	for _, inst := range expired {
		if ctx.Err() != nil {
			return
		}
		if err := m.retireFunctionExecutionOwner(ctx, inst); err != nil {
			m.log.Error("function execution ownership retirement failed", "instance", inst.Lease.Instance, "error", err)
		}
	}
}

func (m *Manager) retireFunctionExecutionOwner(ctx context.Context, inst *Instance) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 3*api.FunctionCancellationGrace)
	defer cancel()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*api.FunctionCancellationGrace)
	defer cleanupCancel()
	id := inst.Lease.Instance
	flight, owner, err := m.beginLiveInstanceTeardown(ctx, id, inst)
	if err != nil {
		return err
	}
	if !owner {
		return flight.err
	}
	code := 0
	defer func() {
		if retErr != nil {
			m.finishInstanceTeardown(id, flight, code, false, false, retErr)
			return
		}
		// Publish completed physical teardown while retaining the stop gate.
		// schedd's synchronous destroy joins this result; replacement boots
		// remain blocked until the owner has observed the failure notification.
		m.mu.Lock()
		flight.exitCode = code
		close(flight.done)
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			if m.instanceStops[id] == flight {
				delete(m.instanceStops, id)
			}
			m.mu.Unlock()
		}()
		m.ReportLivenessFailed(ctx, id, LivenessReasonInfrastructure)
	}()
	code, retErr = m.destroyWithReservation(ctx, cleanupCtx, id, "")
	if retErr == nil {
		m.functionActivity.Forget(id)
	}
	return retErr
}
