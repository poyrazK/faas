package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Integration labels are types, not arbitrary receiver names. Refuse new names
// until reviewed rather than exporting untrusted free-form infrastructure labels.
func knownNotificationIntegration(name string) bool {
	switch name {
	case "email", "pushover", "slack", "webhook", "pagerduty", "opsgenie", "victorops", "wechat", "telegram", "discord", "webex", "sns", "msteams", "msteamsv2", "rocketchat", "jira":
		return true
	default:
		return false
	}
}

func notificationIntegrationMap(rows []promql.VectorSample, err error) (map[string]promql.VectorSample, error) {
	if err != nil {
		return nil, err
	}
	if len(rows) > api.ObsNotificationIntegrationsMax {
		return nil, errors.New("integration bound exceeded")
	}
	out := make(map[string]promql.VectorSample, len(rows))
	for _, row := range rows {
		name := row.Labels["integration"]
		if !knownNotificationIntegration(name) || len(row.Labels) != 1 {
			return nil, errors.New("invalid integration label")
		}
		if _, exists := out[name]; exists {
			return nil, errors.New("duplicate integration")
		}
		out[name] = row
	}
	return out, nil
}

func notificationIntegrations(ctx context.Context, client *promql.Client, metric string, now time.Time) (map[string]api.ObsNotificationSignal, api.ObsNotificationSignal) {
	selector := metric + `{job="alertmanager"}`
	increase := fmt.Sprintf(`increase(%s[%ds])`, selector, int(api.ObsNotificationWindow.Seconds()))
	query := fmt.Sprintf(`sum by (integration) (%s) and on (integration) (count by (integration) (%s) == count by (integration) (%s))`, increase, increase, selector)
	values, err := notificationVector(ctx, client, query)
	observations, obsErr := notificationVector(ctx, client, fmt.Sprintf(`min by (integration) (max_over_time(timestamp(%s)[%ds:%ds]))`, selector, int(api.ObsHealthObservationLookback.Seconds()), int(api.ObsHealthObservationStep.Seconds())))
	vmap, err := notificationIntegrationMap(values, err)
	omap, obsErr := notificationIntegrationMap(observations, obsErr)
	base := notificationSignal(nil, err, nil, obsErr, api.ObsNotificationWindow, now)
	out := map[string]api.ObsNotificationSignal{}
	if err != nil || obsErr != nil {
		return out, base
	}
	names := map[string]bool{}
	for name := range vmap {
		names[name] = true
	}
	for name := range omap {
		names[name] = true
	}
	total := 0.0
	var oldest *time.Time
	complete := len(names) > 0
	for name := range names {
		var v, o []promql.VectorSample
		if row, ok := vmap[name]; ok {
			v = []promql.VectorSample{row}
		}
		if row, ok := omap[name]; ok {
			o = []promql.VectorSample{row}
		}
		signal := notificationSignal(v, nil, o, nil, api.ObsNotificationWindow, now)
		out[name] = signal
		if signal.State != api.ObsHealthSignalHealthy {
			complete = false
			continue
		}
		total += *signal.Value
		if oldest == nil || signal.ObservedAt.Before(*oldest) {
			oldest = signal.ObservedAt
		}
	}
	if len(names) > 0 && !complete {
		base.State, base.Reason = api.ObsHealthSignalUnavailable, "incomplete_integration_evidence"
	}
	if complete && !math.IsInf(total, 0) {
		base.State, base.Reason, base.Value, base.ObservedAt = api.ObsHealthSignalHealthy, "", &total, oldest
	}
	return out, base
}
