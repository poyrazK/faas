package main

import (
	"errors"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

// devLoopMetrics measures the `gregale dev` inner loop: how long a saved
// edit takes to go live (as reported by the CLI) and whether syncs can use a
// live patch (ADR-740) or must rebuild. Every label is bounded.
type devLoopMetrics struct {
	registry   prometheus.Registerer
	syncs      *prometheus.CounterVec
	editToLive *prometheus.HistogramVec
	phases     *prometheus.HistogramVec
	patchPlans *prometheus.CounterVec
}

// Patch plan outcomes for a developer sync.
const (
	devPatchOutcomePublished        = "published"
	devPatchOutcomeIneligible       = "ineligible"
	devPatchOutcomeNoChanges        = "no_changes"
	devPatchOutcomeDeliveryDisabled = "delivery_disabled"
	devPatchOutcomePublishFailed    = "publish_failed"
	devPatchOutcomeUninspected      = "uninspected"
)

var devPatchReasonLabels = map[string]bool{
	api.DevPatchReasonNotRailpack: true, api.DevPatchReasonPlanUnreadable: true,
	api.DevPatchReasonNoSourceLayer: true, api.DevPatchReasonBuildCommand: true,
	api.DevPatchReasonSourceNotDeployed: true, api.DevPatchReasonNoLiveBuild: true,
	api.DevPatchReasonNoBaseManifest: true, api.DevPatchReasonFullSnapshot: true,
	api.DevPatchReasonRebuildInput: true, api.DevPatchReasonUnsupportedEntry: true,
	api.DevPatchReasonTooLarge: true, api.DevPatchReasonUnsupportedVersion: true,
}

func devPatchReasonLabel(reason string) string {
	if reason == "" || devPatchReasonLabels[reason] {
		return reason
	}
	return "other"
}

func newDevLoopMetrics(reg prometheus.Registerer, prefix string) *devLoopMetrics {
	seconds := []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1200}
	m := &devLoopMetrics{
		registry: reg,
		syncs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_dev_syncs_total",
			Help: "Developer syncs reported by `gregale dev`, by status and whether edit-to-live met its target.",
		}, []string{"status", "within_slo"}),
		editToLive: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "_dev_sync_edit_to_live_seconds",
			Help:    "Developer sync edit-to-live time reported by `gregale dev`, by status.",
			Buckets: seconds,
		}, []string{"status"}),
		phases: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "_dev_sync_phase_seconds",
			Help:    "Developer sync phase durations reported by `gregale dev`, by phase and status.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
		}, []string{"phase", "status"}),
		patchPlans: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_dev_patch_plans_total",
			Help: "Developer sync live-patch decisions (ADR-740), by outcome and ineligibility reason.",
		}, []string{"outcome", "reason"}),
	}
	register := func(c prometheus.Collector) prometheus.Collector {
		if err := reg.Register(c); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				return already.ExistingCollector
			}
			panic(err)
		}
		return c
	}
	m.syncs = register(m.syncs).(*prometheus.CounterVec)
	m.editToLive = register(m.editToLive).(*prometheus.HistogramVec)
	m.phases = register(m.phases).(*prometheus.HistogramVec)
	m.patchPlans = register(m.patchPlans).(*prometheus.CounterVec)
	for _, outcome := range []string{devPatchOutcomePublished, devPatchOutcomeNoChanges, devPatchOutcomeDeliveryDisabled, devPatchOutcomePublishFailed, devPatchOutcomeUninspected} {
		m.patchPlans.WithLabelValues(outcome, "")
	}
	return m
}

// observeSync records one CLI-reported sync receipt. Phases are validated
// against devSyncHistoryPhases before this is called.
func (m *devLoopMetrics) observeSync(req api.RecordDevSyncRequest) {
	if m == nil {
		return
	}
	m.syncs.WithLabelValues(req.Status, strconv.FormatBool(req.WithinSLO)).Inc()
	m.editToLive.WithLabelValues(req.Status).Observe((time.Duration(req.EditToLiveMS) * time.Millisecond).Seconds())
	for _, phase := range req.Phases {
		if phase.DurationMS > 0 {
			m.phases.WithLabelValues(phase.Phase, phase.Status).Observe((time.Duration(phase.DurationMS) * time.Millisecond).Seconds())
		}
	}
}

func (m *devLoopMetrics) observePatchPlan(outcome, reason string) {
	if m == nil {
		return
	}
	m.patchPlans.WithLabelValues(outcome, devPatchReasonLabel(reason)).Inc()
}
