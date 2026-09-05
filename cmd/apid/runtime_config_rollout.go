package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeConfigPromotionHealth is shared by the PATCH promotion path and the
// background safety controller. A promotion is allowed only when the current
// canary has a successful daemon acknowledgement and enough healthy traffic.
func (s *server) runtimeConfigPromotionHealth(ctx context.Context, row state.RuntimeConfig) (error, bool) {
	ackStore, ok := s.store.(interface {
		ListRuntimeConfigAcks(context.Context, string, state.RuntimeConfigScope, string) ([]state.RuntimeConfigAck, error)
	})
	if !ok {
		return fmt.Errorf("runtime config acknowledgements are unavailable"), true
	}
	acks, err := ackStore.ListRuntimeConfigAcks(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil {
		return fmt.Errorf("list runtime config acknowledgements: %w", err), true
	}
	applied := 0
	for _, ack := range acks {
		if ack.Version != row.Version {
			continue
		}
		if ack.Status == state.RuntimeConfigAckFailed {
			if ack.Error == "" {
				return fmt.Errorf("daemon %s rejected version %d", ack.Consumer, row.Version), false
			}
			return fmt.Errorf("daemon %s rejected version %d: %s", ack.Consumer, row.Version, ack.Error), false
		}
		if ack.Status == state.RuntimeConfigAckApplied {
			applied++
		}
	}
	if applied == 0 {
		return fmt.Errorf("no daemon has acknowledged version %d", row.Version), false
	}
	provider := runtimeconfig.PrometheusHealthProvider{Client: s.promqlClient, Policy: runtimeconfig.DefaultHealthPolicy}
	snapshot, err := provider.Snapshot(ctx)
	if err != nil {
		return err, true
	}
	return runtimeconfig.DefaultHealthPolicy.Evaluate(snapshot), false
}

func runtimeConfigPromotionProblem(err error, unavailable bool) *api.Problem {
	if unavailable {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Configuration health is unavailable", err.Error())
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict,
		"Configuration promotion blocked by the health gate", err.Error())
}

// runtimeConfigPromotionRequested reports whether a PATCH is widening an
// existing daemon canary to the full fleet. New settings and ordinary edits
// do not need the gate; only a 100% promotion does.
func runtimeConfigPromotionRequested(store state.Store, key string, scope state.RuntimeConfigScope, scopeID string, percent int, ctx context.Context) (state.RuntimeConfig, bool, error) {
	if percent != 100 || scope != state.RuntimeConfigScopeDaemon {
		return state.RuntimeConfig{}, false, nil
	}
	row, err := store.GetRuntimeConfig(ctx, key, scope, scopeID)
	if errors.Is(err, state.ErrRuntimeConfigNotFound) {
		return state.RuntimeConfig{}, false, nil
	}
	if err != nil {
		return state.RuntimeConfig{}, false, err
	}
	return row, row.RolloutPercent > 0 && row.RolloutPercent < 100 && row.Status == state.RuntimeConfigApplied, nil
}
