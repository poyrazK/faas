package githubd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// GitHub's log of the App's webhook deliveries is the only place a delivery
// that never reached githubd shows up: a webhook URL pointing at another host,
// a secret out of sync, or githubd down during a restart. The listener's own
// counters see none of those. HookDeliveryMonitor polls that log with the App
// JWT and exports what it finds; GitHub does not redeliver failed App
// deliveries on its own, so each failed one is also logged with the GUID an
// operator needs to redeliver it.

// DefaultHookDeliveryPollInterval keeps the poll far inside the App's API
// rate limit while catching a broken webhook within minutes.
const DefaultHookDeliveryPollInterval = 5 * time.Minute

// DefaultHookDeliveryWindow is how far back the recent-delivery gauges count.
const DefaultHookDeliveryWindow = time.Hour

// hookDeliveryPageSize is GitHub's maximum page size; one page covers the
// window on any realistic delivery rate.
const hookDeliveryPageSize = 100

// HookDelivery is one row of GET /app/hook/deliveries.
type HookDelivery struct {
	ID             int64     `json:"id"`
	GUID           string    `json:"guid"`
	DeliveredAt    time.Time `json:"delivered_at"`
	Redelivery     bool      `json:"redelivery"`
	Status         string    `json:"status"`
	StatusCode     int       `json:"status_code"`
	Event          string    `json:"event"`
	Action         string    `json:"action"`
	InstallationID int64     `json:"installation_id"`
}

// Failed reports whether GitHub recorded the delivery as not accepted. A
// timeout or refused connection has status code 0.
func (d HookDelivery) Failed() bool { return d.StatusCode < 200 || d.StatusCode > 299 }

// HookDeliverySummary aggregates one poll.
type HookDeliverySummary struct {
	RecentDelivered  int
	RecentFailed     int
	HasLatest        bool
	LatestFailed     bool
	LatestStatusCode int
}

// HookDeliveryMonitor polls the App's delivery log.
type HookDeliveryMonitor struct {
	// MintJWT returns an App JWT (AppAuth.MintAppJWT in production).
	MintJWT func() (string, error)
	Client  HTTPClient
	// BaseURL defaults to GitHubAPI; tests point it at an httptest server.
	BaseURL string
	Window  time.Duration
	Metrics *HookDeliveryMetrics
	// AppConfigMetrics, when set, also checks the App's events and
	// permissions on every poll (app_config.go).
	AppConfigMetrics *AppConfigMetrics
	Log              *slog.Logger
	Now              func() time.Time

	lastLoggedID int64
}

var errHookDeliveriesUnavailable = errors.New("githubd: app hook deliveries unavailable")

// PollOnce fetches the newest page of deliveries, records the summary and
// logs failed deliveries it has not logged before.
func (m *HookDeliveryMonitor) PollOnce(ctx context.Context) (HookDeliverySummary, error) {
	m.checkAppConfig(ctx)
	deliveries, err := m.fetch(ctx)
	if err != nil {
		m.Metrics.observeFailure()
		return HookDeliverySummary{}, err
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	window := m.Window
	if window <= 0 {
		window = DefaultHookDeliveryWindow
	}
	summary := summarizeHookDeliveries(deliveries, now.Add(-window))
	m.Metrics.observe(summary, now)
	m.logNewFailures(deliveries, now.Add(-window))
	return summary, nil
}

func (m *HookDeliveryMonitor) fetch(ctx context.Context) ([]HookDelivery, error) {
	if m.MintJWT == nil {
		return nil, fmt.Errorf("%w: no App JWT", errHookDeliveriesUnavailable)
	}
	jwt, err := m.MintJWT()
	if err != nil {
		return nil, fmt.Errorf("%w: mint App JWT: %w", errHookDeliveriesUnavailable, err)
	}
	base := m.BaseURL
	if base == "" {
		base = GitHubAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/app/hook/deliveries?per_page=%d", base, hookDeliveryPageSize), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %w", errHookDeliveriesUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	client := m.Client
	if client == nil {
		client = NewHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %w", errHookDeliveriesUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GitHub status %d", errHookDeliveriesUnavailable, resp.StatusCode)
	}
	var deliveries []HookDelivery
	if err := decodeGitHubJSON(resp.Body, &deliveries); err != nil {
		return nil, fmt.Errorf("%w: decode: %w", errHookDeliveriesUnavailable, err)
	}
	return deliveries, nil
}

// summarizeHookDeliveries counts deliveries since the cutoff and reads the
// newest one. GitHub returns newest first, but the newest is chosen by ID so
// the result does not depend on that order.
func summarizeHookDeliveries(deliveries []HookDelivery, since time.Time) HookDeliverySummary {
	var summary HookDeliverySummary
	var latest HookDelivery
	for _, d := range deliveries {
		if !d.DeliveredAt.Before(since) {
			if d.Failed() {
				summary.RecentFailed++
			} else {
				summary.RecentDelivered++
			}
		}
		if !summary.HasLatest || d.ID > latest.ID {
			latest = d
			summary.HasLatest = true
		}
	}
	if summary.HasLatest {
		summary.LatestFailed = latest.Failed()
		summary.LatestStatusCode = latest.StatusCode
	}
	return summary
}

// logNewFailures logs each failed delivery once. On the first poll only the
// window is logged, so a restart does not replay days of history.
func (m *HookDeliveryMonitor) logNewFailures(deliveries []HookDelivery, since time.Time) {
	maxID := m.lastLoggedID
	for _, d := range deliveries {
		if d.ID > maxID {
			maxID = d.ID
		}
		if d.ID <= m.lastLoggedID || !d.Failed() || (m.lastLoggedID == 0 && d.DeliveredAt.Before(since)) {
			continue
		}
		if m.Log != nil {
			m.Log.Warn("githubd: GitHub could not deliver an App webhook",
				"guid", logsanitize.Field(d.GUID), "delivery_id", d.ID,
				"event", logsanitize.Field(d.Event), "action", logsanitize.Field(d.Action),
				"status_code", d.StatusCode, "status", logsanitize.Field(d.Status),
				"installation_id", d.InstallationID, "redelivery", d.Redelivery,
				"delivered_at", d.DeliveredAt)
		}
	}
	m.lastLoggedID = maxID
}

// RunHookDeliveryMonitor polls once at start and then every interval until
// ctx is cancelled.
func RunHookDeliveryMonitor(ctx context.Context, m *HookDeliveryMonitor, interval time.Duration, log *slog.Logger) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultHookDeliveryPollInterval
	}
	if log == nil {
		log = slog.Default()
	}
	poll := func() {
		if _, err := m.PollOnce(ctx); err != nil && ctx.Err() == nil {
			log.Warn("githubd: poll App webhook deliveries", "err", err)
		}
	}
	poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// HookDeliveryMetrics exports the App delivery log without a per-delivery
// label.
type HookDeliveryMetrics struct {
	mu               sync.Mutex
	polled           bool
	summary          HookDeliverySummary
	polls            map[string]float64
	lastSuccess      float64
	recentDesc       *prometheus.Desc
	latestFailedDesc *prometheus.Desc
	latestStatusDesc *prometheus.Desc
	pollsDesc        *prometheus.Desc
	lastSuccessDesc  *prometheus.Desc
}

// NewHookDeliveryMetrics creates a collector for a per-daemon registry.
func NewHookDeliveryMetrics() *HookDeliveryMetrics {
	return &HookDeliveryMetrics{
		polls: map[string]float64{"ok": 0, "error": 0},
		recentDesc: prometheus.NewDesc("githubd_github_hook_deliveries_recent",
			"App webhook deliveries GitHub recorded in the last hour, by outcome.", []string{"outcome"}, nil),
		latestFailedDesc: prometheus.NewDesc("githubd_github_hook_latest_delivery_failed",
			"1 when GitHub's newest App webhook delivery got no 2xx response.", nil, nil),
		latestStatusDesc: prometheus.NewDesc("githubd_github_hook_latest_delivery_status_code",
			"HTTP status GitHub recorded for its newest App webhook delivery (0 = timeout or no connection).", nil, nil),
		pollsDesc: prometheus.NewDesc("githubd_github_hook_delivery_polls_total",
			"Polls of GitHub's App webhook delivery log, by result.", []string{"result"}, nil),
		lastSuccessDesc: prometheus.NewDesc("githubd_github_hook_delivery_last_poll_success_timestamp_seconds",
			"Unix time of the last successful poll of GitHub's App webhook delivery log.", nil, nil),
	}
}

func (m *HookDeliveryMetrics) observe(summary HookDeliverySummary, now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polled = true
	m.summary = summary
	m.polls["ok"]++
	m.lastSuccess = float64(now.Unix())
}

func (m *HookDeliveryMetrics) observeFailure() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls["error"]++
}

// Describe implements prometheus.Collector.
func (m *HookDeliveryMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.recentDesc
	ch <- m.latestFailedDesc
	ch <- m.latestStatusDesc
	ch <- m.pollsDesc
	ch <- m.lastSuccessDesc
}

// Collect implements prometheus.Collector. The delivery gauges appear only
// after the first successful poll, so a githubd that cannot read the log does
// not report a healthy webhook.
func (m *HookDeliveryMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for result, value := range m.polls {
		ch <- prometheus.MustNewConstMetric(m.pollsDesc, prometheus.CounterValue, value, result)
	}
	ch <- prometheus.MustNewConstMetric(m.lastSuccessDesc, prometheus.GaugeValue, m.lastSuccess)
	if !m.polled {
		return
	}
	ch <- prometheus.MustNewConstMetric(m.recentDesc, prometheus.GaugeValue, float64(m.summary.RecentDelivered), "delivered")
	ch <- prometheus.MustNewConstMetric(m.recentDesc, prometheus.GaugeValue, float64(m.summary.RecentFailed), "failed")
	latestFailed := 0.0
	if m.summary.LatestFailed {
		latestFailed = 1
	}
	ch <- prometheus.MustNewConstMetric(m.latestFailedDesc, prometheus.GaugeValue, latestFailed)
	ch <- prometheus.MustNewConstMetric(m.latestStatusDesc, prometheus.GaugeValue, float64(m.summary.LatestStatusCode))
}
