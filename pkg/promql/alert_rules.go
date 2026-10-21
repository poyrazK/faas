package promql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AlertRuleGroup struct {
	Name   string            `json:"name"`
	File   string            `json:"file"`
	Rules  []AlertRule       `json:"rules"`
	Labels map[string]string `json:"labels,omitempty"`
}
type AlertRule struct {
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	Query            string            `json:"query"`
	Duration         float64           `json:"duration"`
	DurationReported bool              `json:"-"`
	KeepFiringFor    *float64          `json:"keepFiringFor,omitempty"`
	Labels           map[string]string `json:"labels"`
	LabelsNull       bool              `json:"-"`
	Annotations      map[string]string `json:"annotations"`
	State            string            `json:"state"`
	Health           string            `json:"health"`
	LastError        string            `json:"lastError"`
	LastEvaluation   time.Time         `json:"lastEvaluation"`
	Alerts           []Alert           `json:"alerts"`
}

// Keep absence separate from a reported zero without changing the collector's
// existing Duration value or acceptance policy.
func (r *AlertRule) UnmarshalJSON(data []byte) error {
	type wire AlertRule
	var decoded struct {
		*wire
		Duration *float64        `json:"duration"`
		Labels   json.RawMessage `json:"labels"`
	}
	out := AlertRule{}
	decoded.wire = (*wire)(&out)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Duration != nil {
		out.Duration = *decoded.Duration
		out.DurationReported = true
	}
	if len(decoded.Labels) > 0 {
		out.LabelsNull = bytes.Equal(bytes.TrimSpace(decoded.Labels), []byte("null"))
		if err := json.Unmarshal(decoded.Labels, &out.Labels); err != nil {
			return err
		}
	}
	*r = out
	return nil
}

// AlertRules requires a complete bounded envelope. Warnings or absent arrays
// cannot authorize inferred recovery from an apparently empty result.
func (c *Client) AlertRules(ctx context.Context) ([]AlertRuleGroup, error) {
	return c.ruleInventory(ctx, "alert")
}

// RecordingRules uses the same strict bounds as alert reads. Prometheus omits
// empty recording labels; explicit null remains distinguishable from omission.
func (c *Client) RecordingRules(ctx context.Context) ([]AlertRuleGroup, error) {
	return c.ruleInventory(ctx, "record")
}

func (c *Client) ruleInventory(ctx context.Context, ruleType string) ([]AlertRuleGroup, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("prometheus rules: not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/rules?type="+ruleType, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus rules: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Infos    []string `json:"infos"`
		Data     struct {
			Groups []AlertRuleGroup `json:"groups"`
		} `json:"data"`
	}
	if err := decodePromQLResponse(resp.Body, &payload); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("prometheus rules: invalid response")
	}
	if payload.Status != "success" || payload.Data.Groups == nil || len(payload.Warnings) > 0 || len(payload.Infos) > 0 {
		return nil, fmt.Errorf("prometheus rules: incomplete response")
	}
	rules, alerts := 0, 0
	if err := validatePromQLSeries(len(payload.Data.Groups)); err != nil {
		return nil, err
	}
	for _, group := range payload.Data.Groups {
		if group.Rules == nil {
			return nil, fmt.Errorf("prometheus rules: missing rules")
		}
		rules += len(group.Rules)
		for _, rule := range group.Rules {
			alerts += len(rule.Alerts)
		}
	}
	if err := validatePromQLSeries(rules); err != nil {
		return nil, err
	}
	if err := validatePromQLSeries(alerts); err != nil {
		return nil, err
	}
	return payload.Data.Groups, nil
}
