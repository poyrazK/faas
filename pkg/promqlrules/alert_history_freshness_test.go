package promqlrules_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// The operator alert collector records a snapshot only when every loaded
// alerting rule was evaluated within api.ObsHealthFreshnessMaxAge, so one
// slow alerting group makes it reject nearly every snapshot. A 1h and a 5m
// group took production platform alert history from 97 % of polls recorded
// to under 1 % (Hunt 14 H14-111). An alerting group must evaluate at least
// twice inside that window, so one skipped evaluation stays fresh.
// Recording-only groups are not read by the collector.
func TestAlertingGroupsEvaluateInsideAlertHistoryFreshness(t *testing.T) {
	// prometheus.yml.j2's global evaluation_interval.
	const globalInterval = 15 * time.Second
	limit := api.ObsHealthFreshnessMaxAge / 2
	files := []string{
		filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml"),
		filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "data_repair.rules.yml"),
		filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "pg_backup.rules.yml"),
		filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "bridge.rules.yml"),
		"data_placement.yaml",
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		var doc struct {
			Groups []struct {
				Name     string `yaml:"name"`
				Interval string `yaml:"interval"`
				Rules    []struct {
					Alert string `yaml:"alert"`
				} `yaml:"rules"`
			} `yaml:"groups"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, group := range doc.Groups {
			alerting := false
			for _, rule := range group.Rules {
				alerting = alerting || rule.Alert != ""
			}
			if !alerting {
				continue
			}
			interval := globalInterval
			if group.Interval != "" {
				interval, err = time.ParseDuration(group.Interval)
				if err != nil {
					t.Errorf("%s group %s: interval %q: %v", file, group.Name, group.Interval, err)
					continue
				}
			}
			if interval > limit {
				t.Errorf("%s group %s evaluates alerts every %s; the operator alert collector needs every %s or faster (api.ObsHealthFreshnessMaxAge/2). Keep slow recordings in their own group.", file, group.Name, interval, limit)
			}
		}
	}
}
