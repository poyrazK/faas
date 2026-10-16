package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// AlertmanagerClient reads only the alert-status API. It never reads receiver
// configuration, secret files, silence comments, or notification destinations.
type AlertmanagerClient struct {
	baseURL string
	doer    promql.HTTPDoer
}

func NewAlertmanagerClient(baseURL string, doer promql.HTTPDoer) *AlertmanagerClient {
	if doer == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // Only literal loopback destinations are allowed.
		doer = &http.Client{Transport: transport, Timeout: api.ObsNotificationBudget,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &AlertmanagerClient{baseURL: strings.TrimRight(baseURL, "/"), doer: doer}
}

type alertmanagerAlert struct {
	Labels    map[string]string `json:"labels"`
	StartsAt  time.Time         `json:"startsAt"`
	EndsAt    time.Time         `json:"endsAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Status    struct {
		State       string   `json:"state"`
		SilencedBy  []string `json:"silencedBy"`
		InhibitedBy []string `json:"inhibitedBy"`
	} `json:"status"`
}

func (c *AlertmanagerClient) configuration() api.ObsNotificationAPIStatus {
	if c == nil || c.baseURL == "" || c.baseURL == "disabled" {
		return api.ObsNotificationAPIStatus{State: "disabled", Reason: "not_configured"}
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" {
		return api.ObsNotificationAPIStatus{State: "unavailable", Reason: "invalid_configuration"}
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return api.ObsNotificationAPIStatus{State: "unavailable", Reason: "invalid_configuration"}
	}
	return api.ObsNotificationAPIStatus{State: "healthy"}
}

func (c *AlertmanagerClient) alerts(ctx context.Context, now time.Time) ([]alertmanagerAlert, api.ObsNotificationAPIStatus) {
	status := c.configuration()
	if status.State != "healthy" {
		return nil, status
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsNotificationBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/alerts?active=true&silenced=true&inhibited=true&unprocessed=true", nil)
	if err != nil {
		return nil, api.ObsNotificationAPIStatus{State: "unavailable", Reason: "invalid_configuration"}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, api.ObsNotificationAPIStatus{State: "unavailable", Reason: "request_failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	status = api.ObsNotificationAPIStatus{State: "unavailable", Reason: "invalid_response"}
	if resp.StatusCode != http.StatusOK {
		status.Reason = "http_error"
		return nil, status
	}
	rows, err := decodeAlertmanagerAlerts(resp.Body)
	if err != nil {
		return nil, status
	}
	checked := now.UTC()
	return rows, api.ObsNotificationAPIStatus{State: "healthy", ObservedAt: &checked}
}

func decodeAlertmanagerAlerts(r io.Reader) ([]alertmanagerAlert, error) {
	body, err := io.ReadAll(io.LimitReader(r, api.ObsAlertmanagerResponseMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.ObsAlertmanagerResponseMaxBytes {
		return nil, errors.New("alertmanager response exceeds bound")
	}
	var rows []alertmanagerAlert
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	if rows == nil || len(rows) > api.ObsAlertmanagerAlertsMax {
		return nil, errors.New("incomplete alertmanager snapshot")
	}
	for _, row := range rows {
		if row.Labels["alertname"] == "" || row.StartsAt.IsZero() || row.EndsAt.IsZero() || row.UpdatedAt.IsZero() || row.Status.SilencedBy == nil || row.Status.InhibitedBy == nil {
			return nil, errors.New("incomplete alertmanager alert")
		}
		switch row.Status.State {
		case "active", "suppressed", "unprocessed":
		default:
			return nil, errors.New("unknown alertmanager state")
		}
	}
	return rows, nil
}
