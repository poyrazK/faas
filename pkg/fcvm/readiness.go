package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// ReadinessProbeConfig is the resolved steady-state readiness policy used by
// vmmd. Unlike liveness, a failed readiness probe only withdraws traffic; it
// never destroys or restarts the instance.
type ReadinessProbeConfig struct {
	Path             string
	GRPC             bool
	GRPCService      string
	Port             int
	PeriodSeconds    int
	TimeoutSeconds   int
	FailureThreshold int
}

// ReadinessProbeStarter launches the cmd/vmmd probe loop. The Manager owns the
// context and cancels it on park, destroy, process exit, or daemon shutdown.
type ReadinessProbeStarter func(ctx context.Context, instance string, slot int, appID string, cfg ReadinessProbeConfig)

// WithReadinessProbeStarter attaches vmmd's host-side recurring readiness
// probe implementation.
func (m *Manager) WithReadinessProbeStarter(starter ReadinessProbeStarter) *Manager {
	m.readinessStarter = starter
	return m
}

func readinessProbeConfig(raw json.RawMessage) (ReadinessProbeConfig, error) {
	var override api.DeploymentReadinessProbe
	if err := json.Unmarshal(raw, &override); err != nil {
		return ReadinessProbeConfig{}, fmt.Errorf("decode readiness probe: %w", err)
	}
	pathSet, grpcSet := override.Path != "", override.GRPC != nil
	if pathSet == grpcSet {
		return ReadinessProbeConfig{}, fmt.Errorf("readiness probe must set exactly one of path or grpc")
	}
	if pathSet && (!strings.HasPrefix(override.Path, "/") || strings.ContainsAny(override.Path, "\r\n")) {
		return ReadinessProbeConfig{}, fmt.Errorf("readiness probe path must start with / and contain no line breaks")
	}
	if override.PeriodS < 0 || override.PeriodS > api.MaxReadinessPeriodSeconds {
		return ReadinessProbeConfig{}, fmt.Errorf("readiness probe period is out of range")
	}
	if override.TimeoutS < 0 || override.TimeoutS > 5 {
		return ReadinessProbeConfig{}, fmt.Errorf("readiness probe timeout is out of range")
	}
	if override.FailureThreshold < 0 || override.FailureThreshold > 10 {
		return ReadinessProbeConfig{}, fmt.Errorf("readiness probe failure threshold is out of range")
	}
	cfg := ReadinessProbeConfig{
		Path:             override.Path,
		Port:             netns.AppPort,
		PeriodSeconds:    api.DefaultReadinessPeriodSeconds,
		TimeoutSeconds:   api.DefaultReadinessTimeoutSeconds,
		FailureThreshold: api.DefaultReadinessFailureThreshold,
	}
	if grpcSet {
		cfg.GRPC = true
		cfg.GRPCService = override.GRPC.Service
	}
	if override.PeriodS > 0 {
		cfg.PeriodSeconds = override.PeriodS
	}
	if override.TimeoutS > 0 {
		cfg.TimeoutSeconds = override.TimeoutS
	}
	if override.FailureThreshold > 0 {
		cfg.FailureThreshold = override.FailureThreshold
	}
	return cfg, nil
}

func (m *Manager) startReadinessLoop(ctx context.Context, instance string, slot int, override json.RawMessage) {
	if len(override) == 0 || m.readinessStarter == nil {
		return
	}
	cfg, err := readinessProbeConfig(override)
	if err != nil {
		m.log.Warn("readiness: invalid override; instance remains fail-closed", "instance", instance, "err", err)
		return
	}
	parent := m.lifecycleCtx
	if parent == nil {
		parent = context.WithoutCancel(ctx)
	}
	loopCtx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	inst, ok := m.live[instance]
	if !ok || inst.Paused || inst.AppID == "" {
		m.mu.Unlock()
		cancel()
		return
	}
	appID := inst.AppID
	if inst.Port > 0 && inst.Port <= 65535 {
		cfg.Port = inst.Port
	}
	if m.readinessLoopCancels == nil {
		m.readinessLoopCancels = make(map[string]context.CancelFunc)
	}
	previous := m.readinessLoopCancels[instance]
	m.readinessLoopCancels[instance] = cancel
	m.mu.Unlock()
	if previous != nil {
		previous()
	}
	m.readinessStarter(loopCtx, instance, slot, appID, cfg)
}

func (m *Manager) cancelReadinessLoop(instance string) {
	m.mu.Lock()
	cancel := m.readinessLoopCancels[instance]
	delete(m.readinessLoopCancels, instance)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
