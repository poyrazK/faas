// adr: 570
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// One immutable registration baseline belongs to the process, captured before
// any repair consumer starts. Registration waits until serving listeners bind.
type trafficRuntimeSession struct {
	store      trafficRuntimeObservationStore
	node, boot string
	expected   int64
	mu         sync.Mutex
	epoch      state.GatewayTrafficEpoch
	lost       bool
}

func newTrafficRuntimeSession(ctx context.Context, store trafficRuntimeObservationStore, node string) (*trafficRuntimeSession, error) {
	node = strings.TrimSpace(node)
	if store == nil || node == "" {
		return nil, nil
	}
	opCtx, cancel := context.WithTimeout(ctx, api.TrafficRuntimeObservationTimeout)
	defer cancel()
	baseline, err := store.ReadGatewayTrafficEpoch(opCtx, node)
	if err != nil {
		return nil, fmt.Errorf("capture gateway process registration baseline: %w", err)
	}
	return &trafficRuntimeSession{store: store, node: node, boot: uuid.NewString(), expected: baseline.Generation}, nil
}

func (s *trafficRuntimeSession) register(ctx context.Context) (state.GatewayTrafficEpoch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lost {
		return state.GatewayTrafficEpoch{}, state.ErrGatewayTrafficEpochLost
	}
	if s.epoch.Generation > 0 {
		return s.epoch, nil
	}
	epoch, err := s.store.RegisterGatewayTrafficEpoch(ctx, s.node, s.boot, s.expected)
	if errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		s.lost = true
	}
	if err == nil {
		s.epoch = epoch
	}
	return epoch, err
}

func (s *trafficRuntimeSession) current() (state.GatewayTrafficEpoch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch, s.lost
}

func (s *trafficRuntimeSession) observeError(err error) {
	if errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		s.mu.Lock()
		s.lost = true
		s.mu.Unlock()
	}
}

// The existing replay seams keep independent ledger cursors. Production
// overrides their legacy publishers with this shared process session.
type gatewayPolicyRepairStore struct {
	*state.PgStore
	session *trafficRuntimeSession
}

var (
	_ controlPlaneRepairStore       = (*gatewayPolicyRepairStore)(nil)
	_ edgeRuleRepairStore           = (*gatewayPolicyRepairStore)(nil)
	_ corsPresetRepairStore         = (*gatewayPolicyRepairStore)(nil)
	_ responseCachePurgeRepairStore = (*gatewayPolicyRepairStore)(nil)
)

func (s *gatewayPolicyRepairStore) publish(ctx context.Context, node string, kind state.GatewayPolicyKind, cursor int64) error {
	if s.session == nil {
		return nil
	}
	if node != s.session.node {
		return errors.New("gateway policy publisher node differs from process")
	}
	epoch, lost := s.session.current()
	if lost {
		return state.ErrGatewayTrafficEpochLost
	}
	// Replay may happen before listener binding. The next successful repair
	// tick publishes once registration exists; no legacy row is written.
	if epoch.Generation == 0 {
		return nil
	}
	err := s.ReportGatewayPolicyProgress(ctx, epoch, kind, cursor)
	s.session.observeError(err)
	return err
}

func (s *gatewayPolicyRepairStore) UpsertGatewayControlPlaneWatermark(ctx context.Context, node, _ string, cursor int64) error {
	return s.publish(ctx, node, state.GatewayPolicyControlPlane, cursor)
}
func (s *gatewayPolicyRepairStore) UpsertGatewayEdgeRuleWatermark(ctx context.Context, node, _ string, cursor int64) error {
	return s.publish(ctx, node, state.GatewayPolicyEdgeRules, cursor)
}
func (s *gatewayPolicyRepairStore) UpsertGatewayCorsPresetWatermark(ctx context.Context, node, _ string, cursor int64) error {
	return s.publish(ctx, node, state.GatewayPolicyCorsPresets, cursor)
}
func (s *gatewayPolicyRepairStore) UpsertGatewayResponseCachePurgeWatermark(ctx context.Context, node string, cursor int64) error {
	return s.publish(ctx, node, state.GatewayPolicyCachePurge, cursor)
}
