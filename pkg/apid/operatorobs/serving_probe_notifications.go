package operatorobs

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const notificationRecording = "faas_serving_probe_notification:"

func ServingProbeNotificationChannels() []string { return []string{"heartbeat", "webhook"} }

func ServingProbeNotificationStates() []string {
	return []string{"disabled", "unknown", "last_submission_succeeded", "last_submission_failed"}
}

func ServingProbeNotificationReasons() []string {
	return []string{"", "inventory_unavailable", "inventory_not_configured", "prometheus_not_configured", "zero_expected_probes", "incomplete_evidence", "query_failed", "recording_missing", "recording_not_current", "recording_unknown", "source_unavailable", "metadata_inconsistent"}
}

func servingNotificationQueries() []string {
	return servingNotificationQueriesForNode("")
}

func servingNotificationQueriesForNode(node string) []string {
	// At most 2,000 channel states plus 1,000 run clocks, or two scalars per
	// channel (4,000). Every vector fits the shared 4,096-series bound.
	selectors := []string{
		`faas_serving_probe_notification:state|faas_serving_probe:last_run`,
		`faas_serving_probe_notification:(last_attempt_success|last_attempt_failure_code)`,
		`faas_serving_probe_notification:(last_attempt_timestamp_seconds|last_success_timestamp_seconds)`,
		`faas_serving_probe_notification:evidence_status`,
	}
	queries := make([]string, 0, len(selectors)*2)
	filter := ""
	if node != "" {
		filter = ",probe_node=" + strconv.Quote(node)
	}
	for _, selector := range selectors {
		q := `label_replace({__name__=~"` + selector + `"` + filter + `},"evidence_metric","$1","__name__","(.+)")`
		queries = append(queries, q, "timestamp("+q+")")
	}
	return queries
}

type notificationKey struct{ metric, node, channel string }
type notificationSamples struct {
	values, times map[notificationKey]float64
	reason        string
}

func notificationIndex(rows []promql.VectorSample, metrics []string) (map[notificationKey]float64, bool) {
	if len(rows) > 2*api.ObsServingProbeNotificationChannelsMax {
		return nil, false
	}
	out := make(map[notificationKey]float64, len(rows))
	for _, row := range rows {
		key := notificationKey{row.Labels[probeMetricLabel], row.Labels["probe_node"], row.Labels["channel"]}
		if !slices.Contains(metrics, key.metric) || !ServingProbeNodeValid(key.node) || math.IsNaN(row.Value) || math.IsInf(row.Value, 0) || row.Labels["__name__"] != "" && row.Labels["__name__"] != key.metric {
			return nil, false
		}
		if key.metric == probeRun && key.channel != "" || key.metric != probeRun && !slices.Contains(ServingProbeNotificationChannels(), key.channel) {
			return nil, false
		}
		for name := range row.Labels {
			if !slices.Contains([]string{"__name__", probeMetricLabel, "probe_node", "channel"}, name) {
				return nil, false
			}
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		out[key] = row.Value
	}
	return out, true
}

func notificationSnapshot(values, times servingProbeQuery, metrics []string) notificationSamples {
	out := notificationSamples{reason: "query_failed"}
	if values.err != nil || times.err != nil {
		return out
	}
	var ok bool
	if out.values, ok = notificationIndex(values.rows, metrics); !ok {
		return out
	}
	if out.times, ok = notificationIndex(times.rows, metrics); ok {
		out.reason = ""
	}
	return out
}

func (s notificationSamples) read(key notificationKey, now time.Time, evaluation *time.Time) (float64, *time.Time, string) {
	if s.reason != "" {
		return 0, nil, s.reason
	}
	value, found := s.values[key]
	if !found {
		if _, hasClock := s.times[key]; hasClock {
			return 0, nil, "metadata_inconsistent"
		}
		return 0, nil, "recording_missing"
	}
	at, valid := inputSampleTime(s.times[key], now)
	if !valid || at.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) || evaluation != nil && !at.Equal(*evaluation) {
		return 0, nil, "recording_not_current"
	}
	return value, &at, ""
}

// UnavailableServingProbeNotifications projects a fixed, data-free fallback.
func UnavailableServingProbeNotifications(reason string) *api.ObsServingProbeNotificationHealth {
	return &api.ObsServingProbeNotificationHealth{State: "unknown", Reason: reason, Limit: api.ObsServingProbeNotificationItemsMax, Channels: []api.ObsServingProbeNotification{}}
}

func servingNotificationPolicies(probes []api.ObsServingProbe) map[string]float64 {
	policies := make(map[string]float64)
	for _, probe := range probes {
		if old, found := policies[probe.Node]; found && old != probe.MaxResultAgeSeconds {
			return nil
		}
		policies[probe.Node] = probe.MaxResultAgeSeconds
	}
	return policies
}

func assessServingProbeNotifications(out api.ObsServingHealth, results []servingProbeQuery, now time.Time) *api.ObsServingProbeNotificationHealth {
	if out.Inventory.State != "healthy" || out.Inventory.Configured == nil {
		return UnavailableServingProbeNotifications("inventory_unavailable")
	}
	if !*out.Inventory.Configured {
		return UnavailableServingProbeNotifications("inventory_not_configured")
	}
	policies := servingNotificationPolicies(out.Probes)
	if policies == nil && len(out.Probes) > 0 {
		return UnavailableServingProbeNotifications("metadata_inconsistent")
	}
	frames := servingNotificationFrames(results)
	items := make([]api.ObsServingProbeNotification, 0, len(policies)*len(ServingProbeNotificationChannels()))
	for node, age := range policies {
		for _, channel := range ServingProbeNotificationChannels() {
			items = append(items, assessServingProbeNotification(node, channel, age, frames, now))
		}
	}
	return summarizeServingProbeNotifications(items)
}

func servingNotificationFrames(results []servingProbeQuery) []notificationSamples {
	return []notificationSamples{
		notificationSnapshot(results[0], results[1], []string{notificationRecording + "state", probeRun}),
		notificationSnapshot(results[2], results[3], []string{notificationRecording + "last_attempt_success", notificationRecording + "last_attempt_failure_code"}),
		notificationSnapshot(results[4], results[5], []string{notificationRecording + "last_attempt_timestamp_seconds", notificationRecording + "last_success_timestamp_seconds"}),
		notificationSnapshot(results[6], results[7], []string{notificationRecording + "evidence_status"}),
	}
}

func assessServingProbeNotification(node, channel string, age float64, frames []notificationSamples, now time.Time) api.ObsServingProbeNotification {
	out := api.ObsServingProbeNotification{Node: node, Channel: channel, State: "unknown"}
	value, evaluation, reason := frames[0].read(notificationKey{notificationRecording + "state", node, channel}, now, nil)
	if reason != "" {
		out.Reason = reason
		return out
	}
	if value < 0 || value > 3 || value != math.Trunc(value) {
		out.Reason = "metadata_inconsistent"
		return out
	}
	if value == 1 {
		return assessUnknownServingProbeNotification(node, channel, age, frames, now, evaluation)
	}
	runAt, valid := notificationRunCurrent(node, age, frames[0], now, evaluation)
	if !valid {
		out.Reason = "source_unavailable"
		return out
	}
	enabled := value != 0
	out.State, out.Enabled, out.EvaluatedAt, out.LastRunAt = ServingProbeNotificationStates()[int(value)], &enabled, evaluation, &runAt
	if value == 0 {
		for i, metric := range []string{"last_attempt_success", "last_attempt_failure_code", "last_attempt_timestamp_seconds", "last_success_timestamp_seconds"} {
			key := notificationKey{notificationRecording + metric, node, channel}
			_, hasValue := frames[1+i/2].values[key]
			_, hasClock := frames[1+i/2].times[key]
			if hasValue || hasClock {
				return api.ObsServingProbeNotification{Node: node, Channel: channel, State: "unknown", Reason: "metadata_inconsistent"}
			}
		}
		return out
	}
	out.Reason = fillServingProbeNotification(&out, frames, now)
	if out.Reason != "" || !servingProbeNotificationValid(out, now, age) {
		if out.Reason == "" {
			out.Reason = "metadata_inconsistent"
		}
		return api.ObsServingProbeNotification{Node: node, Channel: channel, State: "unknown", Reason: out.Reason}
	}
	return out
}

func fillServingProbeNotification(out *api.ObsServingProbeNotification, frames []notificationSamples, now time.Time) string {
	fields := []string{"last_attempt_success", "last_attempt_failure_code", "last_attempt_timestamp_seconds", "last_success_timestamp_seconds"}
	for i, field := range fields {
		frame := frames[1+i/2]
		value, _, reason := frame.read(notificationKey{notificationRecording + field, out.Node, out.Channel}, now, out.EvaluatedAt)
		if reason != "" {
			if i == 3 && reason == "recording_missing" && out.State == "last_submission_failed" {
				continue
			}
			return reason
		}
		switch i {
		case 0:
			if value != 0 && value != 1 || (value == 1) != (out.State == "last_submission_succeeded") {
				return "metadata_inconsistent"
			}
		case 1:
			if value < 0 || value >= float64(len(ServingProbeFailureCodes())) || value != math.Trunc(value) {
				return "metadata_inconsistent"
			}
			out.FailureCode = ServingProbeFailureCodes()[int(value)]
		default:
			at, valid := notificationDataClock(value, now)
			if !valid {
				return "metadata_inconsistent"
			}
			if i == 2 {
				out.LastAttemptAt = &at
			} else {
				out.LastSuccessAt = &at
			}
		}
	}
	return ""
}

func notificationRank(state string) int {
	switch state {
	case "last_submission_failed":
		return 0
	case "unknown":
		return 1
	case "last_submission_succeeded":
		return 2
	default:
		return 3
	}
}

func compareServingProbeNotifications(a, b api.ObsServingProbeNotification) int {
	if v := cmp.Compare(notificationRank(a.State), notificationRank(b.State)); v != 0 {
		return v
	}
	if v := cmp.Compare(a.Node, b.Node); v != 0 {
		return v
	}
	return cmp.Compare(a.Channel, b.Channel)
}

func notificationCounts(items []api.ObsServingProbeNotification) api.ObsServingProbeNotificationCounts {
	out := api.ObsServingProbeNotificationCounts{}
	for _, item := range items {
		switch item.State {
		case "disabled":
			out.Disabled++
		case "last_submission_succeeded":
			out.LastSubmissionSucceeded++
		case "last_submission_failed":
			out.LastSubmissionFailed++
		default:
			out.Unknown++
		}
	}
	return out
}

func notificationSummaryState(counts api.ObsServingProbeNotificationCounts, expected int) (string, string) {
	if expected == 0 {
		return "not_applicable", "zero_expected_probes"
	}
	if counts.Unknown == expected {
		return "unknown", "incomplete_evidence"
	}
	if counts.Unknown > 0 {
		return "partial", "incomplete_evidence"
	}
	return "available", ""
}

func summarizeServingProbeNotifications(items []api.ObsServingProbeNotification) *api.ObsServingProbeNotificationHealth {
	counts, expected := notificationCounts(items), len(items)
	state, reason := notificationSummaryState(counts, expected)
	slices.SortFunc(items, compareServingProbeNotifications)
	limit := api.ObsServingProbeNotificationItemsMax
	retained := min(len(items), limit)
	return &api.ObsServingProbeNotificationHealth{State: state, Reason: reason, ExpectedChannels: &expected, Counts: &counts, Limit: limit, Omitted: expected - retained, Truncated: retained < expected, Channels: slices.Clone(items[:retained])}
}

func servingProbeNotificationValid(item api.ObsServingProbeNotification, now time.Time, age float64) bool {
	if !ServingProbeNodeValid(item.Node) || !slices.Contains(ServingProbeNotificationChannels(), item.Channel) || !slices.Contains(ServingProbeNotificationStates(), item.State) || !slices.Contains(ServingProbeNotificationReasons(), item.Reason) {
		return false
	}
	if item.EvidenceReason != "" && (item.State != "unknown" || item.Reason != "recording_unknown" || !slices.Contains(ServingProbeNotificationEvidenceReasons(), item.EvidenceReason)) {
		return false
	}
	if item.State == "unknown" {
		clockValid := item.EvaluatedAt == nil && item.Reason != "recording_unknown" || item.Reason == "recording_unknown" && notificationTimeCurrent(item.EvaluatedAt, now)
		reasonValid := slices.Contains([]string{"query_failed", "recording_missing", "recording_not_current", "recording_unknown", "source_unavailable", "metadata_inconsistent"}, item.Reason)
		return reasonValid && item.Enabled == nil && item.LastRunAt == nil && item.LastAttemptAt == nil && item.LastSuccessAt == nil && item.FailureCode == "" && clockValid
	}
	if item.Reason != "" || item.Enabled == nil || !notificationTimeCurrent(item.EvaluatedAt, now) || !positiveNotificationTime(item.LastRunAt) || item.LastRunAt.After(now.Add(api.ObsHealthClockSkewTolerance)) || item.LastRunAt.After(item.EvaluatedAt.Add(api.ObsHealthClockSkewTolerance)) || float64(now.UnixMilli()-item.LastRunAt.UnixMilli())/1000 > age {
		return false
	}
	if item.State == "disabled" {
		return !*item.Enabled && item.LastAttemptAt == nil && item.LastSuccessAt == nil && item.FailureCode == ""
	}
	if !*item.Enabled || !positiveNotificationTime(item.LastAttemptAt) || item.LastAttemptAt.After(now.Add(api.ObsHealthClockSkewTolerance)) || item.LastAttemptAt.After(item.EvaluatedAt.Add(api.ObsHealthClockSkewTolerance)) || item.LastSuccessAt != nil && (!positiveNotificationTime(item.LastSuccessAt) || item.LastSuccessAt.After(*item.LastAttemptAt)) {
		return false
	}
	if item.State == "last_submission_succeeded" {
		return item.FailureCode == "none" && item.LastSuccessAt != nil && item.LastSuccessAt.Equal(*item.LastAttemptAt)
	}
	return slices.Contains([]string{"dns", "connection", "tls", "timeout", "http_status", "unknown"}, item.FailureCode)
}

func notificationTimeCurrent(at *time.Time, now time.Time) bool {
	return at != nil && !at.IsZero() && !at.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) && !at.After(now.Add(api.ObsHealthClockSkewTolerance))
}

func positiveNotificationTime(at *time.Time) bool {
	return at != nil && at.After(time.Unix(0, 0))
}

func notificationDataClock(value float64, now time.Time) (time.Time, bool) {
	if _, valid := inputSampleTime(value, now); !valid {
		return time.Time{}, false
	}
	seconds := math.Floor(value)
	at := time.Unix(int64(seconds), int64(math.Round((value-seconds)*float64(time.Second)))).UTC()
	return at, positiveNotificationTime(&at)
}

// ServingProbeNotificationsValid is shared by human/JSON CLI rendering. Older
// servers may omit the section; unsafe optional metadata is never printed.
func ServingProbeNotificationsValid(out api.ObsServingHealth) bool {
	d := out.ProbeNotifications
	if d == nil {
		return true
	}
	if d.Limit != api.ObsServingProbeNotificationItemsMax || d.Channels == nil || len(d.Channels) > d.Limit || !slices.Contains(ServingProbeNotificationReasons(), d.Reason) {
		return false
	}
	if d.ExpectedChannels == nil || d.Counts == nil {
		return d.State == "unknown" && slices.Contains([]string{"inventory_unavailable", "inventory_not_configured", "prometheus_not_configured", "metadata_inconsistent"}, d.Reason) && d.ExpectedChannels == nil && d.Counts == nil && len(d.Channels) == 0 && d.Omitted == 0 && !d.Truncated
	}
	policies := servingNotificationPolicies(out.Probes)
	if out.Inventory.State != "healthy" || out.Inventory.Configured == nil || !*out.Inventory.Configured || policies == nil && len(out.Probes) > 0 {
		return false
	}
	expected, c := len(policies)*len(ServingProbeNotificationChannels()), *d.Counts
	values := []int{c.Disabled, c.Unknown, c.LastSubmissionSucceeded, c.LastSubmissionFailed}
	total := 0
	for _, n := range values {
		if n < 0 || n > api.ObsServingProbeNotificationChannelsMax {
			return false
		}
		total += n
	}
	state, reason := notificationSummaryState(c, expected)
	if expected > api.ObsServingProbeNotificationChannelsMax || *d.ExpectedChannels != expected || total != expected || d.State != state || d.Reason != reason || len(d.Channels) != min(expected, d.Limit) || d.Omitted != expected-len(d.Channels) || d.Truncated != (d.Omitted > 0) {
		return false
	}
	seen := make(map[string]bool, len(d.Channels))
	for i, item := range d.Channels {
		age, found := policies[item.Node]
		key := item.Node + "/" + item.Channel
		if !found || seen[key] || !servingProbeNotificationValid(item, out.GeneratedAt, age) || i > 0 && compareServingProbeNotifications(d.Channels[i-1], item) >= 0 {
			return false
		}
		seen[key] = true
	}
	preview := notificationCounts(d.Channels)
	want := api.ObsServingProbeNotificationCounts{}
	left := d.Limit
	want.LastSubmissionFailed = min(left, c.LastSubmissionFailed)
	left -= want.LastSubmissionFailed
	want.Unknown = min(left, c.Unknown)
	left -= want.Unknown
	want.LastSubmissionSucceeded = min(left, c.LastSubmissionSucceeded)
	left -= want.LastSubmissionSucceeded
	want.Disabled = min(left, c.Disabled)
	return preview == want
}
