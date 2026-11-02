package githubd

// The GitHub App's settings live on GitHub, outside every deploy
// (docs/ops/github-app.md). In November 2026 the production App subscribed
// only to push and pull_request, so repository renames and issue callbacks
// never arrived and nothing noticed (ADR-20261101160300). The delivery-log
// monitor also reads GET /app and exports each required event and
// permission the App lacks.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// RequiredAppEvents are the webhook events githubd needs subscribed.
// installation, installation_repositories and installation_target arrive
// without a subscription.
var RequiredAppEvents = []string{"issues", "pull_request", "push", "repository"}

// RequiredAppPermissions maps each repository or organization permission
// githubd uses to the least access it needs.
var RequiredAppPermissions = map[string]string{
	"checks":        "write",
	"contents":      "read",
	"deployments":   "write",
	"issues":        "write",
	"members":       "read",
	"metadata":      "read",
	"pull_requests": "read",
}

// AppConfig is the part of GET /app githubd checks.
type AppConfig struct {
	Events      []string          `json:"events"`
	Permissions map[string]string `json:"permissions"`
}

// AppConfigGap is one required setting the App lacks.
type AppConfigGap struct {
	Kind string // "event" or "permission"
	Name string
}

// Gaps returns the required events and permissions the App lacks, sorted.
func (c AppConfig) Gaps() []AppConfigGap {
	subscribed := make(map[string]bool, len(c.Events))
	for _, event := range c.Events {
		subscribed[event] = true
	}
	var gaps []AppConfigGap
	for _, event := range RequiredAppEvents {
		if !subscribed[event] {
			gaps = append(gaps, AppConfigGap{Kind: "event", Name: event})
		}
	}
	for name, need := range RequiredAppPermissions {
		if !permissionCovers(c.Permissions[name], need) {
			gaps = append(gaps, AppConfigGap{Kind: "permission", Name: name})
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Kind != gaps[j].Kind {
			return gaps[i].Kind < gaps[j].Kind
		}
		return gaps[i].Name < gaps[j].Name
	})
	return gaps
}

func permissionCovers(have, need string) bool {
	switch need {
	case "read":
		return have == "read" || have == "write" || have == "admin"
	case "write":
		return have == "write" || have == "admin"
	}
	return false
}

func (m *HookDeliveryMonitor) fetchAppConfig(ctx context.Context) (AppConfig, error) {
	if m.MintJWT == nil {
		return AppConfig{}, fmt.Errorf("githubd: read App settings: no App JWT")
	}
	jwt, err := m.MintJWT()
	if err != nil {
		return AppConfig{}, fmt.Errorf("githubd: read App settings: mint App JWT: %w", err)
	}
	base := m.BaseURL
	if base == "" {
		base = GitHubAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/app", nil)
	if err != nil {
		return AppConfig{}, fmt.Errorf("githubd: read App settings: %w", err)
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
		return AppConfig{}, fmt.Errorf("githubd: read App settings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return AppConfig{}, fmt.Errorf("githubd: read App settings: GitHub status %d", resp.StatusCode)
	}
	var config AppConfig
	if err := decodeGitHubJSON(resp.Body, &config); err != nil {
		return AppConfig{}, fmt.Errorf("githubd: read App settings: decode: %w", err)
	}
	return config, nil
}

// checkAppConfig records the App's settings gaps; a read failure leaves the
// previous result in place and is logged.
func (m *HookDeliveryMonitor) checkAppConfig(ctx context.Context) {
	if m.AppConfigMetrics == nil {
		return
	}
	config, err := m.fetchAppConfig(ctx)
	if err != nil {
		if m.Log != nil && ctx.Err() == nil {
			m.Log.Warn("githubd: read GitHub App settings", "err", err)
		}
		return
	}
	gaps := config.Gaps()
	m.AppConfigMetrics.observe(gaps)
	if len(gaps) > 0 && m.Log != nil {
		names := make([]string, 0, len(gaps))
		for _, gap := range gaps {
			names = append(names, gap.Kind+":"+gap.Name)
		}
		m.Log.Warn("githubd: the GitHub App lacks settings docs/ops/github-app.md requires", "missing", names)
	}
}

// AppConfigMetrics exports githubd_github_app_config_missing{kind,name}: 1
// for each required event or permission the App lacks, 0 for the others.
type AppConfigMetrics struct {
	mu      sync.Mutex
	checked bool
	missing map[AppConfigGap]bool
	desc    *prometheus.Desc
}

// NewAppConfigMetrics creates a collector for a per-daemon registry.
func NewAppConfigMetrics() *AppConfigMetrics {
	return &AppConfigMetrics{
		desc: prometheus.NewDesc("githubd_github_app_config_missing",
			"1 when the GitHub App lacks a webhook event or permission docs/ops/github-app.md requires.", []string{"kind", "name"}, nil),
	}
}

func (m *AppConfigMetrics) observe(gaps []AppConfigGap) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked = true
	m.missing = make(map[AppConfigGap]bool, len(gaps))
	for _, gap := range gaps {
		m.missing[gap] = true
	}
}

// Describe implements prometheus.Collector.
func (m *AppConfigMetrics) Describe(ch chan<- *prometheus.Desc) { ch <- m.desc }

// Collect implements prometheus.Collector. Nothing is exported before the
// first successful read.
func (m *AppConfigMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.checked {
		return
	}
	for _, event := range RequiredAppEvents {
		ch <- prometheus.MustNewConstMetric(m.desc, prometheus.GaugeValue, boolGauge(m.missing[AppConfigGap{Kind: "event", Name: event}]), "event", event)
	}
	for name := range RequiredAppPermissions {
		ch <- prometheus.MustNewConstMetric(m.desc, prometheus.GaugeValue, boolGauge(m.missing[AppConfigGap{Kind: "permission", Name: name}]), "permission", name)
	}
}

func boolGauge(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
