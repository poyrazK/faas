package sched

import (
	"context"
	"sync"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

type admissionReadyKey struct{}
type admissionReadySignal struct {
	once  sync.Once
	ready chan struct{}
}

// Only the first wake carries this process-local signal. It is published
// after the ledger reservation, complete boot spec, and layer verification,
// outside appMu. Siblings still traverse the ordinary admission pipeline.
func signalAdmissionReady(ctx context.Context) {
	if signal, ok := ctx.Value(admissionReadyKey{}).(*admissionReadySignal); ok {
		signal.once.Do(func() { close(signal.ready) })
	}
}

func initialWakeCount(count int) int {
	if count < 1 {
		return 1
	}
	if count > api.ScaleUpMaxBurstPerTick {
		return api.ScaleUpMaxBurstPerTick
	}
	return count
}

type initialWakeResult struct {
	result WakeResult
	err    error
}

// wakeInitialCapacity overlaps sibling restores only after the first wake
// passes its policy and verification gates. Count is a desired-capacity hint,
// not authorization: every sibling remains subject to the scheduler ledger.
func (e *Engine) wakeInitialCapacity(ctx context.Context, appID, trigger string, count int) ([]WakeResult, error) {
	count = initialWakeCount(count)
	if count == 1 {
		result, err := e.Wake(ctx, appID, "", "", trigger)
		return []WakeResult{result}, err
	}
	signal := &admissionReadySignal{ready: make(chan struct{})}
	first := make(chan initialWakeResult, 1)
	go func() {
		result, err := e.Wake(context.WithValue(ctx, admissionReadyKey{}, signal), appID, "", "", trigger)
		first <- initialWakeResult{result, err}
	}()
	var primary *initialWakeResult
	select {
	case got := <-first:
		primary = &got
		// An existing RUNNING row or pre-admission failure does not authorize
		// burst continuations. A fast completed boot can have signalled already.
		select {
		case <-signal.ready:
		default:
			return []WakeResult{got.result}, got.err
		}
	case <-signal.ready:
	}
	missing := min(count-1, count-e.ledger.Concurrency(appID))
	if missing < 0 {
		missing = 0
	}
	siblings := make(chan initialWakeResult, missing)
	burstCtx := WithBurstPlacementSpread(withScaleOutBurstContinuation(ctx))
	for range missing {
		go func() {
			result, err := e.AdmitInstance(burstCtx, appID, "", "", trigger)
			siblings <- initialWakeResult{result, err}
		}()
	}
	if primary == nil {
		got := <-first
		primary = &got
	}
	results := make([]WakeResult, 0, missing+1)
	var firstErr error
	collect := func(got initialWakeResult) {
		if got.err != nil {
			if firstErr == nil {
				firstErr = got.err
			}
			e.log.Warn("sched: initial burst admission failed", "app", appID, "err", got.err)
		} else if !got.result.AtCapacity && got.result.InstanceID != "" {
			results = append(results, got.result)
		}
	}
	collect(*primary)
	for range missing {
		collect(<-siblings)
	}
	// Independently successful siblings remain useful if another boot failed.
	if len(results) > 0 {
		return results, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrAtCapacity
}

func coordinateWakeResult(result WakeResult) *CoordInstance {
	return &CoordInstance{
		InstanceID: result.InstanceID, NodeID: result.NodeID,
		DeploymentID: result.DeploymentID, WakeID: result.WakeID,
		Port: int32(result.Port), ColdBoot: result.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT,
	}
}
