package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Release gate kinds (app_release_gates.kind, ADR-20261019071734).
const (
	ReleaseGateSLO          = "slo"
	ReleaseGateNewErrors    = "new_errors"
	ReleaseGateCustomMetric = "custom_metric"
	// Synthetic and metric_change gates (ADR-20261022082305).
	ReleaseGateSynthetic    = "synthetic"
	ReleaseGateMetricChange = "metric_change"
)

// ErrReleaseGateLimit means the app already has the maximum number of gates.
var ErrReleaseGateLimit = errors.New("state: release gate limit reached")

// ReleaseGate is one of an app's release gates.
type ReleaseGate struct {
	ID           string
	AccountID    string
	AppID        string
	Name         string
	Kind         string
	SLOID        string
	MaxBurnRate  int
	MaxNewGroups int
	MinEvents    int
	MetricName   string
	Comparison   string
	Threshold    float64
	MinSamples   int
	CreatedBy    string
	CreatedAt    time.Time
	// SyntheticCheckID and MaxFailurePercent configure a synthetic gate;
	// MaxChangePercent a metric_change gate (ADR-20261022082305).
	SyntheticCheckID  string
	MaxFailurePercent int
	MaxChangePercent  int
}

// ReleaseGateSLOTarget is the part of an SLO a gate judges by.
type ReleaseGateSLOTarget struct {
	ID                 string
	SLI                string
	LatencyThresholdMS int
	ObjectiveBP        int
}

// ReleaseGateSLISample is a deployment's requests over a window.
type ReleaseGateSLISample struct {
	Requests     int64
	ServerErrors int64
	SlowRequests int64
}

// ReleaseGateErrorGroup is a new error group's class and event count.
type ReleaseGateErrorGroup struct {
	ErrorClass string
	Events     int64
}

// ReleaseGateStore persists release gates (ADR-20261019071734).
type ReleaseGateStore interface {
	// CreateReleaseGate stores a gate unless the app already has maxGates
	// (ErrReleaseGateLimit); a duplicate name is ErrConflict.
	CreateReleaseGate(ctx context.Context, g ReleaseGate, maxGates int, now time.Time) (ReleaseGate, error)
	ListReleaseGates(ctx context.Context, appID string) ([]ReleaseGate, error)
	// DeleteReleaseGate deletes a gate by name (ErrNotFound otherwise).
	DeleteReleaseGate(ctx context.Context, appID, name string) (ReleaseGate, error)
}

// ReleaseGateEvidenceStore reads the signals gates judge (Postgres only).
type ReleaseGateEvidenceStore interface {
	ReleaseGateSLO(ctx context.Context, appID, sloID string) (ReleaseGateSLOTarget, error)
	ReleaseGateDeploymentSLI(ctx context.Context, appID, deploymentID string, since, until time.Time, latencyThresholdMS int) (ReleaseGateSLISample, error)
	ReleaseGateNewErrorGroups(ctx context.Context, appID, deploymentID string, minEvents int) ([]ReleaseGateErrorGroup, error)
	ReleaseGateCustomMetricWindow(ctx context.Context, appID, name string, since, until time.Time) (samples int64, average float64, err error)
}

var _ ReleaseGateStore = (*MemStore)(nil)

func (m *MemStore) CreateReleaseGate(_ context.Context, g ReleaseGate, maxGates int, now time.Time) (ReleaseGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, existing := range m.releaseGates {
		if existing.AppID != g.AppID {
			continue
		}
		if existing.Name == g.Name {
			return ReleaseGate{}, ErrConflict
		}
		count++
	}
	if count >= maxGates {
		return ReleaseGate{}, ErrReleaseGateLimit
	}
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	g.CreatedAt = now.UTC()
	if m.releaseGates == nil {
		m.releaseGates = map[string]ReleaseGate{}
	}
	m.releaseGates[g.ID] = g
	return g, nil
}

func (m *MemStore) ListReleaseGates(_ context.Context, appID string) ([]ReleaseGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReleaseGate
	for _, g := range m.releaseGates {
		if g.AppID == appID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) DeleteReleaseGate(_ context.Context, appID, name string) (ReleaseGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, g := range m.releaseGates {
		if g.AppID == appID && g.Name == name {
			delete(m.releaseGates, id)
			return g, nil
		}
	}
	return ReleaseGate{}, ErrNotFound
}
