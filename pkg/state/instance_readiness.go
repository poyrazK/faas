package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InstanceReadiness is the latest reversible readiness transition observed
// for a live instance. It is an optional Store capability used by gateway
// cache hydration; readiness is deliberately not folded into instance state.
type InstanceReadiness struct {
	Source  string
	Ready   bool
	At      time.Time
	EventID int64
}

// InstanceReadinessReader is a narrow optional state.Store capability so
// existing Store implementations and test doubles need not grow their base
// interface solely for gateway hydration.
type InstanceReadinessReader interface {
	LatestInstanceReadiness(ctx context.Context, instanceIDs []string) (map[string]InstanceReadiness, error)
}

// InstanceReadinessBySourceReader retains independent probe states when a
// deployment has both a primary-app probe and one or more ingress companions.
type InstanceReadinessBySourceReader interface {
	LatestInstanceReadinessBySource(ctx context.Context, instanceIDs []string) (map[string]map[string]InstanceReadiness, error)
}

type DeploymentReadinessConfig struct {
	AppID, DeploymentID              string
	OverrideReadinessProbe, Sidecars json.RawMessage
}

func (s *PgStore) DeploymentReadinessConfigs(ctx context.Context, deploymentIDs []string) (map[string]DeploymentReadinessConfig, error) {
	if len(deploymentIDs) > api.TrafficReadinessBatchSize {
		return nil, fmt.Errorf("deployment readiness batch exceeds %d identities", api.TrafficReadinessBatchSize)
	}
	if len(deploymentIDs) == 0 {
		return map[string]DeploymentReadinessConfig{}, nil
	}
	ids := make([]pgtype.UUID, 0, len(deploymentIDs))
	for _, id := range deploymentIDs {
		var parsed pgtype.UUID
		if err := parsed.Scan(id); err != nil {
			return nil, fmt.Errorf("read deployment readiness identity: %w", err)
		}
		ids = append(ids, parsed)
	}
	rows, err := sqlc.New().DeploymentReadinessConfigs(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]DeploymentReadinessConfig, len(rows))
	for _, row := range rows {
		out[row.DeploymentID] = DeploymentReadinessConfig{AppID: row.AppID, DeploymentID: row.DeploymentID,
			OverrideReadinessProbe: row.OverrideReadinessProbe, Sidecars: row.Sidecars}
	}
	return out, nil
}

func (s *PgStore) LatestInstanceReadiness(ctx context.Context, instanceIDs []string) (map[string]InstanceReadiness, error) {
	out := make(map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().LatestInstanceReadiness(ctx, s.pool, instanceIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.InstanceID == "" || !row.At.Valid || (row.Status != "ready" && row.Status != "unready") {
			continue
		}
		out[row.InstanceID] = InstanceReadiness{Ready: row.Status == "ready", At: row.At.Time, EventID: row.ID}
	}
	return out, nil
}

func (s *PgStore) LatestInstanceReadinessBySource(ctx context.Context, instanceIDs []string) (map[string]map[string]InstanceReadiness, error) {
	out := make(map[string]map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().LatestInstanceReadinessBySource(ctx, s.pool, instanceIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.InstanceID == "" || row.Source == "" || !row.At.Valid || (row.Status != "ready" && row.Status != "unready") {
			continue
		}
		if out[row.InstanceID] == nil {
			out[row.InstanceID] = make(map[string]InstanceReadiness)
		}
		out[row.InstanceID][row.Source] = InstanceReadiness{Source: row.Source, Ready: row.Status == "ready", At: row.At.Time, EventID: row.ID}
	}
	return out, nil
}

func (m *MemStore) LatestInstanceReadiness(_ context.Context, instanceIDs []string) (map[string]InstanceReadiness, error) {
	out := make(map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range m.events {
		if event.Kind != "wake.sidecar_health" && event.Kind != "wake.app_readiness" {
			continue
		}
		var payload struct {
			InstanceID string `json:"instance_id"`
			Status     string `json:"status"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || (payload.Status != "ready" && payload.Status != "unready") {
			continue
		}
		if _, ok := wanted[payload.InstanceID]; !ok {
			continue
		}
		current, ok := out[payload.InstanceID]
		if !ok || event.At.After(current.At) || (event.At.Equal(current.At) && event.ID > current.EventID) {
			out[payload.InstanceID] = InstanceReadiness{Ready: payload.Status == "ready", At: event.At, EventID: event.ID}
		}
	}
	return out, nil
}

func (m *MemStore) LatestInstanceReadinessBySource(_ context.Context, instanceIDs []string) (map[string]map[string]InstanceReadiness, error) {
	out := make(map[string]map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range m.events {
		if event.Kind != "wake.sidecar_health" && event.Kind != "wake.app_readiness" {
			continue
		}
		var payload struct {
			InstanceID  string `json:"instance_id"`
			SidecarName string `json:"sidecar_name"`
			Status      string `json:"status"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || (payload.Status != "ready" && payload.Status != "unready") {
			continue
		}
		if _, ok := wanted[payload.InstanceID]; !ok {
			continue
		}
		source := "primary_app"
		if event.Kind == "wake.sidecar_health" {
			if payload.SidecarName == "" {
				continue
			}
			source = "sidecar:" + payload.SidecarName
		}
		if out[payload.InstanceID] == nil {
			out[payload.InstanceID] = make(map[string]InstanceReadiness)
		}
		current, ok := out[payload.InstanceID][source]
		if !ok || event.At.After(current.At) || (event.At.Equal(current.At) && event.ID > current.EventID) {
			out[payload.InstanceID][source] = InstanceReadiness{Source: source, Ready: payload.Status == "ready", At: event.At, EventID: event.ID}
		}
	}
	return out, nil
}

// ReadinessTarget identifies the scheduler-selected VM lifetime. Empty WakeID
// admits only unscoped legacy observations; identified wakes require the node.
type ReadinessTarget struct{ AppID, InstanceID, WakeID, NodeID string }

func validateReadinessTargets(targets []ReadinessTarget) error {
	if len(targets) > api.TrafficReadinessBatchSize {
		return fmt.Errorf("target readiness batch exceeds %d identities", api.TrafficReadinessBatchSize)
	}
	seen := make(map[string]ReadinessTarget, len(targets))
	for _, target := range targets {
		if target.AppID == "" || target.InstanceID == "" || target.NodeID == "" {
			return fmt.Errorf("target readiness requires app, instance and node identity")
		}
		if previous, ok := seen[target.InstanceID]; ok && previous != target {
			return fmt.Errorf("target readiness has conflicting instance identity")
		}
		seen[target.InstanceID] = target
	}
	return nil
}

func (s *PgStore) LatestInstanceReadinessForTargets(ctx context.Context, targets []ReadinessTarget) (map[string]map[string]InstanceReadiness, error) {
	if err := validateReadinessTargets(targets); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]InstanceReadiness)
	if len(targets) == 0 {
		return out, nil
	}
	args := sqlc.LatestInstanceReadinessForTargetsParams{}
	for _, target := range targets {
		args.AppIds = append(args.AppIds, target.AppID)
		args.InstanceIds = append(args.InstanceIds, target.InstanceID)
		args.WakeIds = append(args.WakeIds, target.WakeID)
		args.NodeIds = append(args.NodeIds, target.NodeID)
	}
	rows, err := sqlc.New().LatestInstanceReadinessForTargets(ctx, s.pool, args)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.InstanceID == "" || row.Source == "" || !row.At.Valid || (row.Status != "ready" && row.Status != "unready") {
			continue
		}
		if out[row.InstanceID] == nil {
			out[row.InstanceID] = make(map[string]InstanceReadiness)
		}
		out[row.InstanceID][row.Source] = InstanceReadiness{Source: row.Source, Ready: row.Status == "ready", At: row.At.Time, EventID: row.ID}
	}
	return out, nil
}

func (m *MemStore) LatestInstanceReadinessForTargets(_ context.Context, targets []ReadinessTarget) (map[string]map[string]InstanceReadiness, error) {
	if err := validateReadinessTargets(targets); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]InstanceReadiness)
	wanted := make(map[string]ReadinessTarget, len(targets))
	for _, target := range targets {
		wanted[target.InstanceID] = target
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range m.events {
		if event.Kind != "wake.app_readiness" && event.Kind != "wake.sidecar_health" {
			continue
		}
		var payload struct {
			AppID       string `json:"app_id"`
			InstanceID  string `json:"instance_id"`
			WakeID      string `json:"wake_id"`
			NodeID      string `json:"node_id"`
			SidecarName string `json:"sidecar_name"`
			Status      string `json:"status"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || (payload.Status != "ready" && payload.Status != "unready") {
			continue
		}
		target, ok := wanted[payload.InstanceID]
		if !ok || payload.AppID != target.AppID || payload.WakeID != target.WakeID || (target.WakeID != "" && payload.NodeID != target.NodeID) {
			continue
		}
		source := "primary_app"
		if event.Kind == "wake.sidecar_health" {
			if payload.SidecarName == "" {
				continue
			}
			source = "sidecar:" + payload.SidecarName
		}
		if out[payload.InstanceID] == nil {
			out[payload.InstanceID] = make(map[string]InstanceReadiness)
		}
		current, ok := out[payload.InstanceID][source]
		if !ok || event.At.After(current.At) || (event.At.Equal(current.At) && event.ID > current.EventID) {
			out[payload.InstanceID][source] = InstanceReadiness{Source: source, Ready: payload.Status == "ready", At: event.At, EventID: event.ID}
		}
	}
	return out, nil
}
