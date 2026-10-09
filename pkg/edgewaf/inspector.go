package edgewaf

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3"
	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// Observer receives inspection outcomes. *gateway.Metrics satisfies it.
type Observer interface {
	ObserveWAFInspection(appID, outcome string, seconds float64)
	ObserveWAFDetection(appID, category string)
}

// Outcome labels for gateway_waf_inspections_total.
const (
	OutcomeClean      = "clean"
	OutcomeDetected   = "detected"
	OutcomeSampledOut = "sampled_out"
	OutcomeDropped    = "dropped"
	OutcomeError      = "error"
)

// maxTrackedApps bounds the per-app budget map. Past it the map is reset,
// which at worst grants each app one fresh burst.
const maxTrackedApps = 10000

// Inspector is the node-wide WAF worker pool. Submit never blocks: a sample
// above the app's budget is sampled out, and one that finds the queue full
// is dropped. Both are counted.
type Inspector struct {
	obs   Observer
	log   *slog.Logger
	queue chan gateway.WAFSample

	budgetMu sync.Mutex
	budgets  map[string]*rate.Limiter

	engines [api.MaxEdgeWAFParanoiaLevel + 1]lazyEngine
}

type lazyEngine struct {
	once sync.Once
	waf  coraza.WAF
	err  error
}

// New returns an Inspector. Call Run to start its workers.
func New(obs Observer, log *slog.Logger) *Inspector {
	if log == nil {
		log = slog.Default()
	}
	return &Inspector{
		obs:     obs,
		log:     log,
		queue:   make(chan gateway.WAFSample, api.EdgeWAFQueueDepth),
		budgets: map[string]*rate.Limiter{},
	}
}

// Run starts api.EdgeWAFWorkers workers and blocks until ctx is done.
// Samples still queued at shutdown are discarded; inspection is advisory.
func (i *Inspector) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range api.EdgeWAFWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case s := <-i.queue:
					i.inspect(s)
				}
			}
		}()
	}
	wg.Wait()
}

// Submit implements gateway.WAFInspector.
func (i *Inspector) Submit(s gateway.WAFSample) {
	if !i.allow(s.AppID) {
		i.obs.ObserveWAFInspection(s.AppID, OutcomeSampledOut, 0)
		return
	}
	select {
	case i.queue <- s:
	default:
		i.obs.ObserveWAFInspection(s.AppID, OutcomeDropped, 0)
	}
}

func (i *Inspector) allow(appID string) bool {
	i.budgetMu.Lock()
	defer i.budgetMu.Unlock()
	lim, ok := i.budgets[appID]
	if !ok {
		if len(i.budgets) >= maxTrackedApps {
			i.budgets = map[string]*rate.Limiter{}
		}
		lim = rate.NewLimiter(rate.Limit(api.EdgeWAFInspectionsPerAppPerSecond), api.EdgeWAFInspectionsPerAppBurst)
		i.budgets[appID] = lim
	}
	return lim.Allow()
}

func (i *Inspector) engine(paranoiaLevel int) (coraza.WAF, error) {
	if paranoiaLevel < 1 || paranoiaLevel > api.MaxEdgeWAFParanoiaLevel {
		paranoiaLevel = api.EdgeWAFDefaultParanoiaLevel
	}
	e := &i.engines[paranoiaLevel]
	e.once.Do(func() {
		e.waf, e.err = compile(paranoiaLevel)
		if e.err != nil {
			i.log.Error("edge waf rule set failed to compile", "paranoia_level", paranoiaLevel, "err", e.err)
		}
	})
	return e.waf, e.err
}

func (i *Inspector) inspect(s gateway.WAFSample) {
	waf, err := i.engine(s.ParanoiaLevel)
	if err != nil {
		i.obs.ObserveWAFInspection(s.AppID, OutcomeError, 0)
		return
	}
	start := time.Now()
	res, err := evaluate(waf, s)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		i.obs.ObserveWAFInspection(s.AppID, OutcomeError, elapsed)
		i.log.Warn("edge waf inspection failed", "app_id", s.AppID, "edge_rule_id", s.RuleID, "request_id", s.RequestID, "err", err)
		return
	}
	if !res.Detected {
		i.obs.ObserveWAFInspection(s.AppID, OutcomeClean, elapsed)
		return
	}
	i.obs.ObserveWAFInspection(s.AppID, OutcomeDetected, elapsed)
	for _, c := range res.Categories {
		i.obs.ObserveWAFDetection(s.AppID, c)
	}
	// Never log header values, the query string, or matched data: they can
	// carry customer secrets. Rule IDs and categories are enough to tune.
	i.log.Info("edge waf detection",
		"app_id", s.AppID, "account_id", s.AccountID, "edge_rule_id", s.RuleID,
		"request_id", s.RequestID, "method", s.Method, "path", pathOnly(s.URI),
		"score", res.Score, "threshold", s.AnomalyThreshold,
		"crs_rule_ids", res.RuleIDs, "categories", res.Categories,
		"body_truncated", s.BodyTruncated)
}

func pathOnly(uri string) string {
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return ""
	}
	return u.Path
}
