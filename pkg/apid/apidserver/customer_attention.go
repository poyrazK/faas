package apidserver

// Customer attention signals (ADR-20261113123000): apid evaluates which
// customers are probably stuck and exposes the signals to operators
// (GET /v1/admin/obs/attention, the tenant 360) and to support staff (the
// staff inbox and ticket panel).

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/attention"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

var attentionKinds = api.AttentionKinds()

// supportSignalMetrics are low-cardinality: no account or app labels.
type supportSignalMetrics struct {
	apiErrors *prometheus.CounterVec
	open      *prometheus.GaugeVec
	outcomes  *prometheus.CounterVec
}

func newSupportSignalMetrics(reg prometheus.Registerer, prefix string) *supportSignalMetrics {
	m := &supportSignalMetrics{
		apiErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_customer_api_errors_total",
			Help: "Control-plane API problems returned to authenticated customers, by problem code."}, []string{"code"}),
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_customer_attention_signals_open",
			Help: "Open customer attention signals by kind at the latest evaluation."}, []string{"kind"}),
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_customer_attention_outcomes_total",
			Help: "Customer attention signals by kind and what support did (contacted, a dismiss outcome, or resolved_on_own)."}, []string{"kind", "outcome"}),
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
	m.apiErrors = register(m.apiErrors).(*prometheus.CounterVec)
	m.open = register(m.open).(*prometheus.GaugeVec)
	m.outcomes = register(m.outcomes).(*prometheus.CounterVec)
	for _, kind := range attentionKinds {
		m.open.WithLabelValues(kind)
	}
	return m
}

// supportSignalsHolder holds the metrics atomically: WithOpsMetrics may run
// after the evaluator has started.
type supportSignalsHolder struct {
	atomic.Pointer[supportSignalMetrics]
}

// countCustomerError increments the per-code counter. Codes come from
// api.WriteProblem callers, a fixed set compiled into apid.
func (s *server) countCustomerError(code string) {
	if m := s.supportSignals.Load(); m != nil {
		m.apiErrors.WithLabelValues(code).Inc()
	}
}

func (s *server) observeAttention(result attention.Result) {
	m := s.supportSignals.Load()
	if m == nil {
		return
	}
	for _, kind := range attentionKinds {
		m.open.WithLabelValues(kind).Set(float64(result.OpenByKind[kind]))
		if n := result.ResolvedOnOwnByKind[kind]; n > 0 {
			m.outcomes.WithLabelValues(kind, api.AttentionOutcomeResolvedOnOwn).Add(float64(n))
		}
	}
}

// countAttentionOutcome counts one staff action on a signal.
func (s *server) countAttentionOutcome(kind, outcome string) {
	if m := s.supportSignals.Load(); m != nil && validAttentionKind(kind) {
		m.outcomes.WithLabelValues(kind, outcome).Inc()
	}
}

// configureAttention starts the evaluator; without a pool it is nil.
func (s *server) configureAttention(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	s.attention = attention.New(pool, log, s.observeAttention)
	if s.attention != nil {
		go s.attention.Run(ctx)
	}
}

// obsListAttention handles GET /v1/admin/obs/attention?kind=&state=&account_id=&limit=.
func (s *server) obsListAttention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	query := r.URL.Query()
	filter := attention.Filter{Kind: query.Get("kind")}
	if raw := query.Get("account_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account_id", "account_id must be a UUID"))
			return
		}
		filter.AccountID = id.String()
	}
	if filter.Kind != "" && !validAttentionKind(filter.Kind) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad kind", "kind must be one of "+strings.Join(attentionKinds, ", ")))
		return
	}
	switch st := query.Get("state"); st {
	case "", "active":
	case api.AttentionStateOpen, api.AttentionStateDismissed, api.AttentionStateResolved:
		filter.States = []string{st}
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad state", "state must be active, open, dismissed or resolved"))
		return
	}
	filter.Limit = api.AttentionListMax
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.AttentionListMax {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad limit", "limit must be 1 to "+strconv.Itoa(api.AttentionListMax)))
			return
		}
		filter.Limit = n
	}
	items, err := s.attention.List(r.Context(), filter)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list attention signals"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsAttentionListResponse{Items: items, Limit: filter.Limit})
}

func validAttentionKind(kind string) bool {
	for _, k := range attentionKinds {
		if k == kind {
			return true
		}
	}
	return false
}
