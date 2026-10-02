// adr: 397
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmd/failureoutbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

func sendFailureReport(ctx context.Context, target string, tlsConfig *tls.Config, r failureoutbox.Report) error {
	conn, err := wire.DialContext(ctx, target, tlsConfig)
	if err != nil {
		return fmt.Errorf("vmmd: dial failure-report recipient: %w", err)
	}
	defer func() { _ = conn.Close() }()
	cli := scheddpb.NewScheddClient(conn)
	var ok bool
	switch r.Kind {
	case failureoutbox.Liveness:
		ack, callErr := cli.ReportLivenessFailed(ctx, &scheddpb.LivenessFailedReport{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, Reason: r.Reason})
		err, ok = callErr, ack.GetOk() && ack.GetApplied()
	case failureoutbox.WorkloadOOM:
		ack, callErr := cli.ReportWorkloadOOM(ctx, &scheddpb.ReportWorkloadOOMRequest{InstanceId: r.InstanceID, SourceNodeId: r.SourceNodeID, PeakMb: r.PeakMB, PlanMb: r.PlanMB})
		err, ok = callErr, ack.GetOk() && ack.GetApplied()
	default:
		return errors.New("vmmd: unknown failure-report kind")
	}
	if err != nil {
		return fmt.Errorf("vmmd: deliver failure report: %w", err)
	}
	if !ok {
		return errors.New("vmmd: failure report awaiting scheduler application")
	}
	return nil
}

func wireFailureReports(sourceNodeID string, mgr *fcvm.Manager, cfg *Config, deps runDeps, log *slog.Logger) (*failureoutbox.Outbox, error) {
	if deps.scheddTarget == "" {
		return nil, nil
	}
	if sourceNodeID == "" {
		return nil, errors.New("vmmd: durable failure reporting requires a resolved compute node ID")
	}
	outbox, err := failureoutbox.Open(cfg.FailureReportDir, ownedFailureReportSender(mgr, deps.scheddTarget, deps.scheddClientTLS), log)
	if err != nil {
		return nil, fmt.Errorf("vmmd: open failure outbox: %w", err)
	}
	enqueue := func(r failureoutbox.Report) {
		r.SourceNodeID = sourceNodeID
		if err := outbox.Enqueue(r); err != nil {
			log.Error("vmmd: failure report persistence pending", "instance_id", r.InstanceID, "kind", r.Kind, "err", err)
		}
	}
	mgr.WithLivenessSink(func(_ context.Context, instanceID, reason string) {
		enqueue(failureoutbox.Report{InstanceID: instanceID, Kind: failureoutbox.Liveness, Reason: reason})
	})
	mgr.WithWorkloadOOMSink(func(_ context.Context, instanceID string, peakMB, planMB int) {
		enqueue(failureoutbox.Report{InstanceID: instanceID, Kind: failureoutbox.WorkloadOOM, PeakMB: uint32(peakMB), PlanMB: uint32(planMB)})
	})
	log.Info("vmmd: durable failure reports wired", "target", deps.scheddTarget, "pending", outbox.Pending())
	return outbox, nil
}

// Remember reconciliation for this Manager lifetime: after a confirmed destroy
// an acknowledgement can be lost, and the next retry must still reach schedd.
// A fresh Manager never inherits this permission from an old daemon's spool.
func ownedFailureReportSender(mgr *fcvm.Manager, target string, tlsConfig *tls.Config) failureoutbox.Sender {
	var reconciled sync.Map
	return func(ctx context.Context, r failureoutbox.Report) error {
		if r.Recovered {
			if _, ok := reconciled.Load(r.InstanceID); !ok {
				if !mgr.HasInstanceOwnership(r.InstanceID) {
					return errors.New("vmmd: recovered failure report requires guest ownership reconciliation")
				}
				reconciled.Store(r.InstanceID, struct{}{})
			}
		}
		return sendFailureReport(ctx, target, tlsConfig, r)
	}
}
