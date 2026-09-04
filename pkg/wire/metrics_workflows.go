package wire

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// WorkflowMetrics bundles all Prometheus metrics for durable workflows (ADR-081 §8).
type WorkflowMetrics struct {
	RunsTotal           *prometheus.CounterVec
	StepsTotal          *prometheus.CounterVec
	StepDurationSeconds *prometheus.HistogramVec
	RunDurationSeconds  *prometheus.HistogramVec
	AwaitingEvents      *prometheus.GaugeVec
	DeadLetterTotal     *prometheus.CounterVec
	DispatchLagSeconds  *prometheus.HistogramVec
}

// NewWorkflowMetrics creates and registers workflow metric families with the given registerer.
func NewWorkflowMetrics(reg prometheus.Registerer) *WorkflowMetrics {
	m := &WorkflowMetrics{
		RunsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "workflow_runs_total",
				Help: "Total number of completed workflow runs by app, plan, and outcome.",
			},
			[]string{"app", "plan", "outcome"},
		),
		StepsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "workflow_steps_total",
				Help: "Total number of completed workflow steps by app, plan, step, and outcome.",
			},
			[]string{"app", "plan", "step", "outcome"},
		),
		StepDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "workflow_step_duration_seconds",
				Help:    "Execution duration of individual workflow steps in seconds.",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
			},
			[]string{"app", "plan", "step"},
		),
		RunDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "workflow_run_duration_seconds",
				Help:    "Total wall-clock execution duration of completed workflow runs in seconds.",
				Buckets: []float64{0.5, 1, 5, 10, 30, 60, 300, 900, 1800, 3600},
			},
			[]string{"app", "plan"},
		),
		AwaitingEvents: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "workflow_awaiting_events",
				Help: "Current count of workflow runs parked in awaiting_event state.",
			},
			[]string{"app", "plan"},
		),
		DeadLetterTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "workflow_dead_letter_total",
				Help: "Total number of dead-lettered workflow runs by reason.",
			},
			[]string{"app", "plan", "reason"},
		),
		DispatchLagSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "workflow_dispatch_lag_seconds",
				Help:    "Delay between scheduled_for and actual step execution dispatch in seconds.",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			},
			[]string{"plan"},
		),
	}

	if reg != nil {
		reg.MustRegister(
			m.RunsTotal,
			m.StepsTotal,
			m.StepDurationSeconds,
			m.RunDurationSeconds,
			m.AwaitingEvents,
			m.DeadLetterTotal,
			m.DispatchLagSeconds,
		)
	}

	return m
}

// ObserveRunComplete records a finished workflow run.
func (m *WorkflowMetrics) ObserveRunComplete(app, plan, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	m.RunsTotal.WithLabelValues(app, plan, outcome).Inc()
	m.RunDurationSeconds.WithLabelValues(app, plan).Observe(duration.Seconds())
}

// ObserveStepComplete records a step execution outcome and duration.
func (m *WorkflowMetrics) ObserveStepComplete(app, plan, step, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	m.StepsTotal.WithLabelValues(app, plan, step, outcome).Inc()
	m.StepDurationSeconds.WithLabelValues(app, plan, step).Observe(duration.Seconds())
}

// ObserveDeadLetter records a dead-letter event.
func (m *WorkflowMetrics) ObserveDeadLetter(app, plan, reason string) {
	if m == nil {
		return
	}
	m.DeadLetterTotal.WithLabelValues(app, plan, reason).Inc()
}
