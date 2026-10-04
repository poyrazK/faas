// adr: 531
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type shutdownBudgetContextKey struct{}

func TestGatewayShutdownBudget(t *testing.T) {
	for _, mode := range []string{"startup_error", "normal_drain", "overall_expired"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.WithValue(t.Context(), shutdownBudgetContextKey{}, "correlation"))
			cancel()
			budget := &gatewayShutdownBudget{}
			if mode != "startup_error" {
				started := time.Now()
				budget.beginDrain()
				ceiling := time.Duration(api.GatewayDrainGraceSeconds+api.GatewayShutdownCleanupGraceSeconds) * time.Second
				if budget.overallDeadline.Before(started) || budget.overallDeadline.After(time.Now().Add(ceiling)) {
					t.Fatal("overall shutdown ceiling changed")
				}
			}
			if mode == "overall_expired" {
				budget.overallDeadline = time.Now().Add(-time.Second)
			}
			before := time.Now()
			first, endFirst := budget.context(parent)
			defer endFirst()
			second, endSecond := budget.context(parent)
			defer endSecond()
			firstDeadline, firstOK := first.Deadline()
			secondDeadline, secondOK := second.Deadline()
			if !firstOK || !secondOK || !firstDeadline.Equal(secondDeadline) || firstDeadline.After(before.Add(time.Duration(api.GatewayShutdownCleanupGraceSeconds)*time.Second+time.Second)) || first.Value(shutdownBudgetContextKey{}) != "correlation" {
				t.Fatalf("cleanup budget first=%v second=%v", firstDeadline, secondDeadline)
			}
			if mode == "overall_expired" {
				if !errors.Is(first.Err(), context.DeadlineExceeded) {
					t.Fatal("expired overall deadline was renewed")
				}
			} else if first.Err() != nil || second.Err() != nil {
				t.Fatal("daemon cancellation prevented cleanup")
			}
			endFirst()
			if mode != "overall_expired" && second.Err() != nil {
				t.Fatal("one consumer canceled another's cleanup")
			}
		})
	}
}
