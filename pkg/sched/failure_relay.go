package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Instance failure kinds carried by InstanceFailureReport.
const (
	InstanceFailureLiveness    = "liveness"
	InstanceFailureWorkloadOOM = "workload_oom"
)

// InstanceFailureReport is a vmmd-observed terminal failure that the
// receiving schedd cannot apply because another schedd owns the app
// (issue #3359). It mirrors the ReportLivenessFailed and ReportWorkloadOOM
// RPC payloads.
type InstanceFailureReport struct {
	InstanceID   string `json:"instance_id"`
	SourceNodeID string `json:"source_node_id,omitempty"`
	AppID        string `json:"app_id"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason,omitempty"`
	PeakMB       int    `json:"peak_mb,omitempty"`
	PlanMB       int    `json:"plan_mb,omitempty"`
}

type failureReportSourceKey struct{}

// ErrFailureReportSourceChanged refuses a report observed before node migration.
var ErrFailureReportSourceChanged = errors.New("sched: failure report source node changed")

// WithFailureReportSourceNode carries the wire-bound observer through owner
// relays and the lock-held Engine reread without changing legacy Engine APIs.
func WithFailureReportSourceNode(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, failureReportSourceKey{}, nodeID)
}

// ValidateFailureReportSource leaves legacy reports unbound. New durable vmmd
// reports always carry a resolved node ID; an instance ID survives migration.
func ValidateFailureReportSource(ctx context.Context, ins state.Instance) error {
	source, _ := ctx.Value(failureReportSourceKey{}).(string)
	if source != "" && source != ins.NodeID {
		return fmt.Errorf("%w: instance %s", ErrFailureReportSourceChanged, ins.ID)
	}
	return nil
}

func (r InstanceFailureReport) validate() error {
	if r.InstanceID == "" || r.AppID == "" {
		return errors.New("sched: instance failure report requires instance_id and app_id")
	}
	switch r.Kind {
	case InstanceFailureLiveness, InstanceFailureWorkloadOOM:
		return nil
	default:
		return fmt.Errorf("sched: instance failure report: unknown kind %q", r.Kind)
	}
}

// RelayInstanceFailure broadcasts r for the schedd that owns the app. The
// caller has already established that this schedd hosts the instance but
// does not own the app.
func (e *Engine) RelayInstanceFailure(ctx context.Context, r InstanceFailureReport) error {
	if err := r.validate(); err != nil {
		return err
	}
	if e.notif == nil {
		return errors.New("sched: relay instance failure: no notifier configured")
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("sched: relay instance failure: encode: %w", err)
	}
	if err := e.notif.Notify(ctx, db.NotifyInstanceFailureRelayed, string(payload)); err != nil {
		return fmt.Errorf("sched: relay instance failure %s: %w", r.InstanceID, err)
	}
	e.log.Info("sched: relayed instance failure to owning schedd",
		"instance", r.InstanceID, "app", r.AppID, "kind", r.Kind, "reason", r.Reason)
	return nil
}

// HandleRelayedInstanceFailure applies a relayed report when this schedd
// owns the instance's app and discards it otherwise. Ownership is decided
// from the stored rows, not the payload, so a stale or forged app_id
// cannot redirect a destroy.
func (e *Engine) HandleRelayedInstanceFailure(ctx context.Context, payload string) error {
	var r InstanceFailureReport
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return fmt.Errorf("sched: decode relayed instance failure: %w", err)
	}
	if err := r.validate(); err != nil {
		return err
	}
	ctx = WithFailureReportSourceNode(ctx, r.SourceNodeID)
	ins, err := e.store.InstanceByID(ctx, r.InstanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: relayed instance failure: load instance %s: %w", r.InstanceID, err)
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: relayed instance failure: load app %s: %w", ins.AppID, err)
	}
	if !e.ownsApp(app) {
		return nil
	}
	switch r.Kind {
	case InstanceFailureWorkloadOOM:
		return e.DestroyForWorkloadOOMFailure(ctx, r.InstanceID, r.PeakMB, r.PlanMB)
	default:
		return e.DestroyForLivenessFailure(ctx, r.InstanceID, r.Reason)
	}
}
