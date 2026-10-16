package operatorobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

var alertNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var alertComponentPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]*$`)

// CollectAlertsOnce records only complete, fresh, healthy rule evaluations.
// No request handler writes history, and neither missing rules nor failed
// observations can manufacture a resolution.
func CollectAlertsOnce(ctx context.Context, store state.OperatorAlertHistoryStore, client *promql.Client, now time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	pollCtx, cancel := context.WithTimeout(ctx, api.ObsAlertCollectionBudget)
	defer cancel()
	groups, err := client.AlertRules(pollCtx)
	reason := "source_read_failed"
	var snapshot state.OperatorAlertSnapshot
	if err == nil {
		snapshot, err = alertSnapshot(groups, now)
		reason = "snapshot_rejected"
	}
	if err == nil {
		err = store.RecordOperatorAlertSnapshot(pollCtx, snapshot)
		reason = "persistence_failed"
		if errors.Is(err, state.ErrInvalidArgument) {
			reason = "snapshot_rejected"
		}
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		reason = "timeout"
	}
	var diagnostics *api.ObsAlertCollectionRejection
	if reason == "snapshot_rejected" {
		d := collectionRejection(groups, now)
		if api.ValidObsAlertCollectionRejection(now, d) {
			diagnostics = &d
		}
	}
	// The poll budget may have expired. Saving its outcome gets a fresh,
	// independently bounded context; shutdown still leaves missing evidence.
	failureCtx, cancelFailure := context.WithTimeout(ctx, api.ObsAlertCollectionOutcomeWriteBudget)
	defer cancelFailure()
	var writeErr error
	if diagnostics != nil {
		writeErr = store.RecordOperatorAlertRejection(failureCtx, now, *diagnostics)
	} else {
		// Unrepresentable diagnostics must not erase the failure receipt.
		writeErr = store.RecordOperatorAlertFailure(failureCtx, now, reason)
	}
	if writeErr != nil {
		return fmt.Errorf("record failed alert collection: %w", writeErr)
	}
	return fmt.Errorf("collect platform alert history: %w", err)
}

func RunAlertCollector(ctx context.Context, store state.OperatorAlertHistoryStore, client *promql.Client, log *slog.Logger, metrics *MaintenanceMetrics) {
	enabled := client != nil && client.BaseURL() != ""
	metrics.setCollectionEnabled(enabled)
	if !enabled {
		return
	}
	ticker := time.NewTicker(api.ObsAlertCollectionInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		err := CollectAlertsOnce(ctx, store, client, now)
		metrics.record("collection", now, err)
		if err != nil && ctx.Err() == nil {
			log.Warn("operator alert history collection failed", "reason", "collection_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func alertSnapshot(groups []promql.AlertRuleGroup, now time.Time) (state.OperatorAlertSnapshot, error) {
	out := state.OperatorAlertSnapshot{ObservedAt: now, Rules: []state.OperatorAlertRuleObservation{}, Episodes: []state.OperatorAlertEpisode{}}
	for _, group := range groups {
		if group.Name == "" || group.File == "" {
			return out, errors.New("rule group identity missing")
		}
		for _, rule := range group.Rules {
			if rule.Type != "alerting" {
				return out, errors.New("unexpected rule type")
			}
			if rule.Health != "ok" || rule.LastError != "" || rule.LastEvaluation.IsZero() || rule.LastEvaluation.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) || rule.LastEvaluation.After(now.Add(api.ObsHealthClockSkewTolerance)) {
				return out, errors.New("rule evaluation unavailable or stale")
			}
			if !alertNamePattern.MatchString(rule.Name) || len(rule.Name) > api.ObsAlertNameMaxBytes || rule.Query == "" || math.IsNaN(rule.Duration) || math.IsInf(rule.Duration, 0) || rule.Duration < 0 || rule.Alerts == nil {
				return out, errors.New("invalid alert rule")
			}
			encoded, err := json.Marshal([]any{group.File, group.Name, rule.Name, rule.Query, rule.Duration, rule.Labels})
			if err != nil {
				return out, fmt.Errorf("alert rule identity: %w", err)
			}
			observation := state.OperatorAlertRuleObservation{Key: fmt.Sprintf("%x", sha256.Sum256(encoded)), EvaluatedAt: rule.LastEvaluation.UTC().Truncate(time.Microsecond), EpisodeIDs: []string{}}
			firing, pending := 0, 0
			for _, alert := range rule.Alerts {
				if alert.Labels["alertname"] != rule.Name || alert.ActiveAt.IsZero() || alert.ActiveAt.After(rule.LastEvaluation) {
					return out, errors.New("invalid active alert")
				}
				switch alert.State {
				case "firing":
					firing++
				case "pending":
					pending++
				default:
					return out, errors.New("invalid alert state")
				}
				if alert.State != "firing" || rule.Name == "FaasWatchdog" || alert.Labels["severity"] == "info" {
					continue
				}
				episode := retainedAlert(alert, rule, observation.Key)
				out.Episodes = append(out.Episodes, episode)
				observation.EpisodeIDs = append(observation.EpisodeIDs, episode.ID)
			}
			consistent := false
			switch rule.State {
			case "firing":
				consistent = firing > 0
			case "pending":
				consistent = pending > 0 && firing == 0
			case "inactive":
				consistent = firing+pending == 0
			}
			if !consistent {
				return out, errors.New("incomplete alert rule state")
			}
			out.Rules = append(out.Rules, observation)
		}
	}
	if len(out.Rules) == 0 {
		return out, errors.New("no alert rules observed")
	}
	return out, nil
}

func retainedAlert(alert promql.Alert, rule promql.AlertRule, ruleKey string) state.OperatorAlertEpisode {
	labels := map[string]string{}
	for key, value := range alert.Labels {
		if key != "__name__" && key != "alertstate" {
			labels[key] = value
		}
	}
	labels["alertstate"] = "firing"
	item := projectAlert(promql.VectorSample{Labels: labels, Value: float64(alert.ActiveAt.UnixMilli()) / 1000})
	component := alert.Labels["component"]
	if len(component) > api.ObsAlertComponentMaxBytes || !alertComponentPattern.MatchString(component) {
		component = ""
	}
	started := alert.ActiveAt.UTC().Truncate(time.Microsecond)
	seriesID := strings.TrimPrefix(item.ID, "platform_alert/")
	book := safeRunbookURL(alert.Annotations["runbook_url"])
	if book == "" {
		book = safeRunbookURL(rule.Annotations["runbook_url"])
	}
	if book == "" {
		book = "/docs/ops/alert-cookbook.md"
	}
	return state.OperatorAlertEpisode{ID: state.OperatorAlertEpisodeID(ruleKey, seriesID, started), SeriesID: seriesID, RuleKey: ruleKey,
		Name: rule.Name, Component: component, Severity: item.Severity, AccountID: item.AccountID, AppID: item.AppID, NodeID: item.NodeID,
		RunbookURL: book, StartedAt: started, LastEvaluatedAt: rule.LastEvaluation.UTC().Truncate(time.Microsecond)}
}
