package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func noSleep(context.Context, time.Duration) error { return nil }

// production-us hunt #5 (H5-29): twelve concurrent workflow runs on a
// max_concurrency=1 app each tried to wake while the single instance was
// still waking; three steps failed with plan_limit_concurrency and burned a
// retry. A synthetic delivery now waits for the capacity already on its way.
func TestWakeForSynthWaitsOutTransientRefusals(t *testing.T) {
	for _, refusal := range []error{
		api.NewProblem(429, api.CodePlanLimitConcur, "Concurrency limit", "max_concurrency is 1; 1 already live"),
		api.NewProblem(503, api.CodeWaitForWarm, "Wait for warm", "scale-out cooldown"),
	} {
		calls := 0
		res, err := wakeForSynth(context.Background(), func(context.Context) (synthWakeResult, error) {
			calls++
			if calls < 3 {
				return synthWakeResult{}, refusal
			}
			return synthWakeResult{instanceID: "i-1"}, nil
		}, noSleep)
		if err != nil || res.instanceID != "i-1" || calls != 3 {
			t.Fatalf("%v: res=%+v err=%v calls=%d, want the waking instance on the third call", refusal, res, err, calls)
		}
	}
}

func TestWakeForSynthReturnsOtherErrorsAtOnce(t *testing.T) {
	calls := 0
	boom := errors.New("node unreachable")
	_, err := wakeForSynth(context.Background(), func(context.Context) (synthWakeResult, error) {
		calls++
		return synthWakeResult{}, boom
	}, noSleep)
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and the original error", err, calls)
	}
}

func TestWakeForSynthStopsWithTheCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	refusal := api.NewProblem(429, api.CodePlanLimitConcur, "Concurrency limit", "at cap")
	_, err := wakeForSynth(ctx, func(context.Context) (synthWakeResult, error) {
		return synthWakeResult{}, refusal
	}, synthWakeSleep)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want the caller's cancellation", err)
	}
}
