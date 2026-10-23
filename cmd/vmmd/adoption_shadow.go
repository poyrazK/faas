package main

// vmmd restart adoption ADR, slices 1 and 2. With adoption_record on, ready
// instances get version-7 adoption blocks. With adopt_running_vms on,
// startup adopts every survivor whose block proves ownership before serving
// anything; refused survivors stay quarantined as before. Otherwise startup
// classifies every record in a background dry run and changes nothing.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type adoptionMetrics struct {
	instances *prometheus.CounterVec
	refused   *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	epoch     prometheus.Gauge
	fenced    prometheus.Counter
}

func newAdoptionMetrics(reg prometheus.Registerer) *adoptionMetrics {
	m := &adoptionMetrics{
		instances: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_adoption_instances_total",
			Help: "Journal records classified at vmmd startup, by outcome (adoptable, adopted, refused, stopped); dry_run=true while adoption is shadow-only.",
		}, []string{"outcome", "dry_run"}),
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_adoption_refused_total",
			Help: "Journal records refused adoption at vmmd startup, by the first failed proof step.",
		}, []string{"reason", "dry_run"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "vmmd_adoption_duration_seconds",
			Help:    "Time to classify every journal record at vmmd startup.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"dry_run"}),
		epoch: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_ownership_epoch",
			Help: "The node ownership epoch this vmmd claimed at startup (0 when it did not adopt).",
		}),
		fenced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_adoption_fenced_total",
			Help: "Startups that adopted nothing because the node's epoch was advanced outside this node's claims.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.instances, m.refused, m.duration, m.epoch, m.fenced)
	}
	return m
}

// startAdoption turns on adoption blocks when configured, then either adopts
// survivors (synchronously: it must finish before prepared networks and RPC
// admission) or runs the dry run in the background.
//
// Adoption first claims the node's ownership epoch. A fenced or failed claim
// adopts nothing: every survivor stays quarantined, as without adoption.
// The returned report is what AdoptionReport serves; it is nil when no epoch
// was claimed.
func startAdoption(ctx context.Context, mgr *fcvm.Manager, writeRecords, adopt bool, claimEpoch func(context.Context) (ownershipClaim, error), metrics *adoptionMetrics, log *slog.Logger) (*vmmdgrpc.AdoptionReport, error) {
	if writeRecords {
		if err := mgr.EnableAdoptionRecords(ctx); err != nil {
			return nil, err
		}
		log.Info("vmmd: writing version-7 adoption records")
	}
	var report *vmmdgrpc.AdoptionReport
	if adopt {
		claim, err := claimEpoch(ctx)
		if claim.Epoch > 0 {
			metrics.epoch.Set(float64(claim.Epoch))
			// Before any RPC can lease a slot: the report's fence.
			mgr.TrackRestartLeases()
			report = &vmmdgrpc.AdoptionReport{NodeID: claim.NodeID, Epoch: claim.Epoch,
				Incarnation: claim.Incarnation, PredecessorIncarnation: claim.Predecessor}
		}
		if err != nil {
			if errors.Is(err, errAdoptionFenced) {
				metrics.fenced.Inc()
				if report != nil {
					report.Fenced = true
				}
			}
			log.Error("vmmd: not adopting restart survivors", "epoch", claim.Epoch, "err", err)
			adopt = false
		}
	}
	if adopt {
		started := time.Now()
		rep, err := mgr.AdoptRestartSurvivors(ctx)
		if err != nil {
			return nil, fmt.Errorf("vmmd: adopt restart survivors: %w", err)
		}
		recordAdoption(rep, time.Since(started), "false", metrics, log)
		report.Verdicts = rep.Verdicts
		return report, nil
	}
	go func() {
		started := time.Now()
		rep, err := mgr.DryRunAdoption(ctx)
		if err != nil {
			log.Warn("vmmd: adoption dry run failed", "err", err)
			return
		}
		recordAdoption(rep, time.Since(started), "true", metrics, log)
	}()
	return report, nil
}

// recordAdoption exports and logs one pass; dryRun is the metric label.
func recordAdoption(rep fcvm.AdoptionDryRunReport, took time.Duration, dryRun string, metrics *adoptionMetrics, log *slog.Logger) {
	outcome, verb := "adoptable", "vmmd: adoption dry run"
	if dryRun == "false" {
		outcome, verb = "adopted", "vmmd: adoption"
	}
	metrics.duration.WithLabelValues(dryRun).Observe(took.Seconds())
	metrics.instances.WithLabelValues(outcome, dryRun).Add(float64(rep.Adoptable))
	refused := 0
	for reason, n := range rep.Refused {
		metrics.refused.WithLabelValues(reason, dryRun).Add(float64(n))
		refused += n
	}
	metrics.instances.WithLabelValues("refused", dryRun).Add(float64(refused))
	metrics.instances.WithLabelValues("stopped", dryRun).Add(float64(rep.Stopped))
	metrics.instances.WithLabelValues("killed", dryRun).Add(float64(rep.Killed))
	for _, v := range rep.Verdicts {
		switch {
		case !v.Adoptable && v.Reason == "" && v.Resolved == fcvm.AdoptionResolvedStopped:
			log.Info(verb+" finished an interrupted stop", "instance", v.Instance)
		case !v.Adoptable && v.Reason == "" && v.Resolved == fcvm.AdoptionResolvedKilled:
			log.Info(verb+" killed an app task or execution whose stream ended", "instance", v.Instance)
		case !v.Adoptable:
			log.Info(verb+" refused", "instance", v.Instance, "reason", v.Reason, "detail", v.Detail)
		case v.Resolved != "":
			log.Info(verb+" resolved an interrupted operation", "instance", v.Instance, "resolved", v.Resolved)
		}
	}
	log.Info(verb, outcome, rep.Adoptable, "refused", refused, "stopped", rep.Stopped, "killed", rep.Killed,
		"fence_repairs", rep.FenceRepairs, "refused_by_reason", rep.Refused, "duration_ms", took.Milliseconds())
}

// adoptingStopGrace bounds how long a stopping vmmd that adopts on restart
// waits for open RPCs (adoption ADR §2 "vmmd's stop is bounded too").
// Streams and long polls (ForwardHTTPStream, WaitJobExit, Logs,
// AttachAppTask) would otherwise hold the stop until systemd's 90 s
// timeout, while the guests themselves keep running for the successor.
const adoptingStopGrace = 2 * time.Second

// grpcStopper is the part of *grpc.Server stopGRPC uses.
type grpcStopper interface {
	GracefulStop()
	Stop()
}

// stopGRPC stops the vmmd gRPC server. Without adoption it waits for every
// RPC, as before. With adoption it waits adoptingStopGrace and then ends the
// rest: their clients see UNAVAILABLE and retry against the successor.
func stopGRPC(srv grpcStopper, adopting bool, log *slog.Logger) {
	if !adopting {
		srv.GracefulStop()
		return
	}
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	timer := time.NewTimer(adoptingStopGrace)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		log.Warn("vmmd: ending open RPCs for restart", "grace", adoptingStopGrace)
		srv.Stop()
		<-stopped
	}
}

// announceRestart sets the node's planned-restart notice as an adopting vmmd
// stops (adoption ADR §3), so schedd retries wakes and ignores failed pings
// until the successor serves. A crash sets none. ctx is already cancelled at
// shutdown, so the write gets its own short deadline.
func announceRestart(store any, nodeID string, log *slog.Logger) {
	notices, ok := store.(state.VMMDRestartNoticeStore)
	if !ok || nodeID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := notices.SetComputeNodeVMMDRestartNotice(ctx, nodeID, api.VMMDRestartNoticeSeconds); err != nil {
		log.Warn("vmmd: planned-restart notice not set", "node_id", nodeID, "err", err)
		return
	}
	log.Info("vmmd: planned-restart notice set", "node_id", nodeID, "seconds", api.VMMDRestartNoticeSeconds)
}

// endRestartNotice clears the notice once this vmmd has adopted and serves.
func endRestartNotice(ctx context.Context, store any, nodeID string, log *slog.Logger) {
	notices, ok := store.(state.VMMDRestartNoticeStore)
	if !ok || nodeID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := notices.ClearComputeNodeVMMDRestartNotice(ctx, nodeID); err != nil {
		log.Warn("vmmd: planned-restart notice not cleared; it expires on its own", "node_id", nodeID, "err", err)
	}
}
