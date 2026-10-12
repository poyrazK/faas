package main

import (
	"context"
	"errors"
	"fmt"
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
	var offset time.Duration
	defer func() {
		if active != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if clearErr := client.ClearScenarioTestChaos(cleanupCtx, runID); clearErr != nil {
				err = errors.Join(err, fmt.Errorf("clear chaos phase %q: %w", phases[activeIndex].Name, clearErr))
			} else {
				clock.finish(time.Now())
				if !activeCaptured {
					if captureErr := captureTestChaosMatches(cleanupCtx, client, runID, active); captureErr != nil {
						err = errors.Join(err, fmt.Errorf("capture chaos phase %q matches: %w", phases[activeIndex].Name, captureErr))
					}
				}
			}
			result.Phases[activeIndex] = active
		}
		clock.finish(time.Now())
	}()

	totalDuration := time.Duration(0)
	for _, phase := range phases {
		totalDuration += phase.Duration
	}
	for i, phase := range phases {
		phaseTransitioned := false
		if i == 0 {
			clock.start(startedAt)
		} else {
			if !waitUntilTestChaosSchedule(ctx, startedAt.Add(offset)) {
				return result, ctx.Err()
			}
			if active != nil {
				if len(phase.Rules) > 0 {
					return result, fmt.Errorf("fault phase %q must be followed by a no-rule phase before phase %q", phases[activeIndex].Name, phase.Name)
				}
				if clearErr := client.ClearScenarioTestChaos(ctx, runID); clearErr != nil {
					return result, fmt.Errorf("clear chaos before phase %q: %w", phase.Name, clearErr)
				}
				clock.transition(i, time.Now())
				phaseTransitioned = true
				activeCaptured = true
				captureErr := captureTestChaosMatches(ctx, client, runID, active)
				result.Phases[activeIndex] = active
				if captureErr != nil {
					return result, fmt.Errorf("chaos phase %q: %w", phases[activeIndex].Name, captureErr)
				}
				active, activeIndex, activeCaptured = nil, -1, false
			}
			if len(phase.Rules) > 0 {
				plan, planErr := (&testChaosSpec{Duration: totalDuration.String(), Rules: phase.Rules}).plan()
				if planErr != nil {
					return result, fmt.Errorf("prepare chaos phase %q: %w", phase.Name, planErr)
				}
				installed, installErr := client.InjectScenarioTestChaos(ctx, runID, api.InjectScenarioTestChaosRequest{
					DurationMS: plan.DurationMS,
					Rules:      scenarioChaosAPIRules(plan.Rules),
				})
				if installErr != nil {
					return result, fmt.Errorf("install chaos phase %q: %w", phase.Name, installErr)
				}
				active = newTestChaosEvidence(&testChaosSpec{Rules: phase.Rules}, plan, installed)
				activeIndex, activeCaptured = i, false
				result.Phases[i] = active
			}
			if !phaseTransitioned {
				clock.transition(i, time.Now())
			}
		}
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
