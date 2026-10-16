package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testChaosScheduleResult struct {
	Phases []*testChaosEvidence
}

func runTestChaosSchedule(ctx context.Context, client *Client, runID string, phases []testLoadChaosPhase, clock *testLoadPhaseClock, startedAt time.Time) (result testChaosScheduleResult, err error) {
	result.Phases = make([]*testChaosEvidence, len(phases))
	var active *testChaosEvidence
	activeIndex := -1
	activeCaptured := false
	installInFlight := false
	var offset time.Duration
	defer func() {
		if active != nil || installInFlight {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			phaseName := "unknown"
			if activeIndex >= 0 && activeIndex < len(phases) {
				phaseName = phases[activeIndex].Name
			}
			if clearErr := client.ClearScenarioTestChaos(cleanupCtx, runID); clearErr != nil {
				err = errors.Join(err, fmt.Errorf("clear chaos phase %q: %w", phaseName, clearErr))
			} else {
				clock.finish(time.Now())
				if active != nil && !activeCaptured {
					if captureErr := captureTestChaosMatches(cleanupCtx, client, runID, active); captureErr != nil {
						err = errors.Join(err, fmt.Errorf("capture chaos phase %q matches: %w", phaseName, captureErr))
					}
				}
			}
			if active != nil {
				result.Phases[activeIndex] = active
			}
		}
		clock.finish(time.Now())
	}()

	totalDuration := time.Duration(0)
	for _, phase := range phases {
		totalDuration += phase.Duration
	}
	for i, phase := range phases {
		if i > 0 && !waitUntilTestChaosSchedule(ctx, startedAt.Add(offset)) {
			return result, ctx.Err()
		}
		release := func() {}
		if clock != nil && clock.correlationGate != nil {
			if i > 0 {
				clock.correlationGate.Lock()
			}
			var once sync.Once
			release = func() { once.Do(clock.correlationGate.Unlock) }
		}
		defer release()
		if i == 0 {
			clock.start(startedAt)
		} else {
			if !waitUntilTestChaosSchedule(ctx, startedAt.Add(offset)) {
				return result, ctx.Err()
			}
			if active != nil && len(phase.Rules) > 0 {
				return result, fmt.Errorf("fault phase %q must be followed by a no-rule phase before phase %q", phases[activeIndex].Name, phase.Name)
			}
			// Without a correlation gate, requests continue while the control API
			// applies the next plan. Attribute that transition to the fault: a
			// recovery window starts only after the clear, while a fault window
			// starts before its install.
			if active != nil {
				if clearErr := client.ClearScenarioTestChaos(ctx, runID); clearErr != nil {
					return result, fmt.Errorf("clear chaos before phase %q: %w", phase.Name, clearErr)
				}
				clock.transition(i, time.Now())
				release()
				activeCaptured = true
				captureErr := captureTestChaosMatches(ctx, client, runID, active)
				result.Phases[activeIndex] = active
				if captureErr != nil {
					return result, fmt.Errorf("chaos phase %q: %w", phases[activeIndex].Name, captureErr)
				}
				active, activeIndex, activeCaptured = nil, -1, false
			} else {
				clock.transition(i, time.Now())
			}
			if len(phase.Rules) > 0 {
				plan, planErr := (&testChaosSpec{Duration: totalDuration.String(), Rules: phase.Rules}).plan()
				if planErr != nil {
					return result, fmt.Errorf("prepare chaos phase %q: %w", phase.Name, planErr)
				}
				// A canceled request may still have reached the API and installed
				// the plan before its response was lost. Ensure the deferred cleanup
				// clears that uncertain installation too.
				installInFlight = true
				activeIndex = i
				installed, installErr := client.InjectScenarioTestChaos(ctx, runID, api.InjectScenarioTestChaosRequest{
					DurationMS: plan.DurationMS,
					Rules:      scenarioChaosAPIRules(plan.Rules),
				})
				if installErr != nil {
					return result, fmt.Errorf("install chaos phase %q: %w", phase.Name, installErr)
				}
				active = newTestChaosEvidence(&testChaosSpec{Rules: phase.Rules}, plan, installed)
				activeIndex, activeCaptured = i, false
				installInFlight = false
				result.Phases[i] = active
			}
		}
		release()
		offset += phase.Duration
	}
	if !waitUntilTestChaosSchedule(ctx, startedAt.Add(offset)) {
		return result, ctx.Err()
	}
	clock.finish(time.Now())
	return result, nil
}

func waitUntilTestChaosSchedule(ctx context.Context, target time.Time) bool {
	delay := time.Until(target)
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
