package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// synthWakeWait bounds how long a synthetic delivery waits for capacity that
// is already on its way. The scheduler's own deadline still applies.
const synthWakeWait = 25 * time.Second

type synthWakeResult struct {
	instanceID, nodeID, deploymentID, wakeID string
	port                                     int
	identity                                 api.PlatformIdentity
}

type synthWakeFunc func(ctx context.Context) (synthWakeResult, error)

// synthWakeTransient reports a wake refused only because capacity is
// arriving: the app is at its concurrency cap with an instance still waking,
// or inside its scale-out cooldown. Public requests queue behind that wake;
// workflow steps, crons and queue deliveries burned a retry on it instead
// (production-us hunt #5, H5-29).
func synthWakeTransient(err error) bool {
	if p := api.AsProblem(err); p != nil {
		return p.Code == api.CodePlanLimitConcur || p.Code == api.CodeWaitForWarm
	}
	return err != nil && (strings.Contains(err.Error(), api.CodePlanLimitConcur) || strings.Contains(err.Error(), api.CodeWaitForWarm))
}

// wakeForSynth retries a transiently refused wake until the waking instance
// can serve it (the scheduler's Wake returns the running instance), the
// caller's context ends, or synthWakeWait elapses.
func wakeForSynth(ctx context.Context, wake synthWakeFunc, sleep func(context.Context, time.Duration) error) (synthWakeResult, error) {
	deadline := time.Now().Add(synthWakeWait)
	backoff := 200 * time.Millisecond
	for {
		res, err := wake(ctx)
		if err == nil || !synthWakeTransient(err) || time.Now().Add(backoff).After(deadline) {
			return res, err
		}
		if waitErr := sleep(ctx, backoff); waitErr != nil {
			return res, errors.Join(err, waitErr)
		}
		backoff = min(backoff*2, 2*time.Second)
	}
}

func synthWakeSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// synthScheddWake adapts the schedd client's wake RPCs to wakeForSynth,
// preferring the identity-carrying form when the client offers it.
func synthScheddWake(cli scheddgrpc.ScheddClient, appID, deploymentID, scope string) synthWakeFunc {
	return func(ctx context.Context) (synthWakeResult, error) {
		var r synthWakeResult
		var err error
		if rich, ok := cli.(interface {
			WakeWithIdentity(context.Context, string, string, string) (string, string, string, string, int, api.PlatformIdentity, error)
		}); ok {
			r.instanceID, r.nodeID, r.deploymentID, r.wakeID, r.port, r.identity, err = rich.WakeWithIdentity(ctx, appID, deploymentID, scope)
		} else {
			r.instanceID, r.nodeID, r.deploymentID, r.wakeID, r.port, err = cli.Wake(ctx, appID, deploymentID, scope)
		}
		return r, err
	}
}
