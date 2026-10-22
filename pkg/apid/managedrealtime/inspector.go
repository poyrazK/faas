package managedrealtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// InspectorURL links the owner realtime inspector at one channel cursor.
func InspectorURL(slug, endpoint, channel, after string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/realtime?" + url.Values{"endpoint_id": {endpoint}, "channel": {channel}, "after": {after}}.Encode()
}

// InspectorDeadLetterExportURL links the dashboard dead-letter export.
func InspectorDeadLetterExportURL(slug, endpoint, channel, group string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/realtime/dead-letters/export?" + url.Values{"endpoint_id": {endpoint}, "channel": {channel}, "group": {group}, "limit": {strconv.Itoa(api.RealtimeConsumerGroupDeadLetterExportMaxRows)}}.Encode()
}

// LoadInspector fills the owner realtime inspector for an app the caller has
// already loaded under account ownership checks. Session, CSRF and rendering
// stay with the dashboard, which ADR-982 lifts out of apidserver last.
func (s *Service) LoadInspector(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	s.loadInspectorEndpoint(r, acct, app, data)
	if data.EndpointID != "" {
		s.loadInspectorInbox(r, data)
	}
	if data.GroupName != "" && data.GroupError == "" {
		s.loadInspectorReplayJobs(r, data)
	}
}

func (s *Service) loadInspectorEndpoint(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	endpoints, ok := s.Host.Store().(state.ManagedRealtimeEndpointStore)
	if !ok {
		data.ErrorMessage = "Realtime endpoint data is unavailable."
		return
	}
	rows, err := endpoints.ListManagedRealtimeEndpointsForApp(r.Context(), app.ID)
	if err != nil {
		data.ErrorMessage = "Realtime endpoints are temporarily unavailable."
		return
	}
	selected := r.URL.Query().Get("endpoint_id")
	for _, ep := range rows {
		if ep.AppID == app.ID && ep.AccountID == acct.ID {
			data.Endpoints = append(data.Endpoints, dashboard.RealtimeInspectorEndpoint{ID: ep.ID, Enabled: ep.Enabled})
			if selected == "" {
				selected = ep.ID
			}
		}
	}
	if selected == "" {
		return
	}
	found := false
	for _, ep := range data.Endpoints {
		if ep.ID == selected {
			found = true
		}
	}
	if !found {
		data.ErrorMessage = "Endpoint is unavailable."
		return
	}
	data.EndpointID = selected
	s.loadInspectorPublisherCredentials(r, data)
	if !s.loadInspectorChannels(r, data) {
		return
	}
	s.loadInspectorSources(r, acct, app, data)
	s.loadInspectorWebhooks(r, acct, app, data)
}

// loadInspectorChannels reports false when channel listing failed and the
// remaining endpoint sections should not load.
func (s *Service) loadInspectorChannels(r *http.Request, data *dashboard.RealtimeInspectorData) bool {
	directory, ok := s.Host.Store().(state.RealtimeInspectorStore)
	if !ok {
		data.ErrorMessage = "Channel listing is unavailable."
		return false
	}
	var err error
	data.Channels, err = directory.ListRealtimeInspectorChannels(r.Context(), data.EndpointID)
	if err != nil {
		data.ErrorMessage = "Channel listing is temporarily unavailable."
		return false
	}
	data.DeliverySequence = r.URL.Query().Get("delivery_sequence")
	data.Channel = r.URL.Query().Get("channel")
	if data.Channel == "" && len(data.Channels) > 0 {
		data.Channel = data.Channels[0].Channel
	}
	if data.Channel != "" {
		s.loadInspectorSchedules(r, data)
		s.loadInspectorHistory(r, data)
		s.loadInspectorGroups(r, data)
	}
	return true
}

func (s *Service) loadInspectorHistory(r *http.Request, data *dashboard.RealtimeInspectorData) {
	history, ok := s.Host.Store().(state.ManagedRealtimeHistoryStore)
	if !ok {
		data.ErrorMessage = "Retained history is unavailable."
		return
	}
	if after := r.URL.Query().Get("after"); after != "" {
		data.After = after
	}
	after, err := strconv.ParseInt(data.After, 10, 64)
	if err != nil || after < 0 {
		data.ErrorMessage = "Sequence must be a non-negative integer."
		return
	}
	page, err := history.ReadManagedRealtimeChannelHistory(r.Context(), data.EndpointID, data.Channel, after, 50)
	if errors.Is(err, state.ErrManagedRealtimeHistoryInvalid) {
		baseline, baselineErr := history.ReadManagedRealtimeChannelHistory(r.Context(), data.EndpointID, data.Channel, 0, 1)
		if baselineErr == nil && after > baseline.LatestSequence {
			page = baseline
			err = nil
			data.Gap = true
		}
	}
	if err != nil {
		data.ErrorMessage = "History is unavailable for this channel and cursor."
		return
	}
	data.Oldest = strconv.FormatInt(page.OldestSequence, 10)
	data.Latest = strconv.FormatInt(page.LatestSequence, 10)
	data.Gap = data.Gap || page.HistoryUnavailable || after > page.LatestSequence
	floor := max(page.OldestSequence-1, 0)
	data.BrowseURL = InspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(floor, 10))
	if data.Gap {
		return
	}
	for _, message := range page.Messages {
		data.Messages = append(data.Messages, projectInspectorMessage(message))
	}
	if len(page.Messages) > 0 {
		last := page.Messages[len(page.Messages)-1].Sequence
		if last < page.LatestSequence {
			data.NextURL = InspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(last, 10))
		}
	}
}

func projectInspectorMessage(message state.ManagedRealtimeChannelMessage) dashboard.RealtimeInspectorMessage {
	metadata, _ := json.MarshalIndent(message.Metadata, "", "  ")
	payload := base64.StdEncoding.EncodeToString(message.Data)
	if !message.Binary && utf8.Valid(message.Data) {
		payload = string(message.Data)
		var pretty bytes.Buffer
		if json.Indent(&pretty, message.Data, "", "  ") == nil {
			payload = pretty.String()
		}
	}
	expired := message.Expired(time.Now().UTC())
	expiresAt := ""
	if message.ExpiresAt != nil {
		expiresAt = message.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if expired {
		payload = ""
		metadata = nil
	}
	return dashboard.RealtimeInspectorMessage{TTLSeconds: message.TTLSeconds, ExpiresAt: expiresAt, Expired: expired, Sequence: strconv.FormatInt(message.Sequence, 10), CreatedAt: message.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"), Metadata: string(metadata), Payload: payload, Binary: message.Binary, Version: message.Version, Event: message.MessageEvent, Deleted: message.Deleted, TargetID: message.TargetMessageID}
}

func (s *Service) loadInspectorSchedules(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.ManagedRealtimeScheduleStore)
	if !ok {
		data.ScheduleError = "Scheduled publishing is unavailable."
		return
	}
	rows, err := store.ListManagedRealtimeSchedules(r.Context(), data.EndpointID, data.Channel)
	if err != nil {
		data.ScheduleError = "Scheduled events are temporarily unavailable."
		return
	}
	data.ScheduleTotal = len(rows)
	for _, row := range rows {
		switch row.Status {
		case "pending":
			data.PendingSchedules++
		case "paused":
			data.PausedSchedules++
		case "failed":
			data.FailedSchedules++
		}
		item := dashboard.RealtimeInspectorSchedule{ID: row.ID, Status: row.Status, Group: row.Group, DeliverAt: row.DeliverAt.UTC().Format(time.RFC3339Nano), LastError: row.LastError, Version: row.Version, Attempts: row.Attempts, Occurrence: row.Occurrence, Sequence: row.Sequence, IntervalSeconds: row.IntervalSeconds}
		if row.NextAttemptAt != nil {
			item.NextAttemptAt = row.NextAttemptAt.UTC().Format(time.RFC3339Nano)
		}
		data.Schedules = append(data.Schedules, item)
	}
}

func (s *Service) loadInspectorSources(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	observer, ok := s.Host.Store().(state.RealtimeInspectorOutboxStore)
	if !ok {
		return
	}
	var err error
	data.Sources, err = observer.ListRealtimeInspectorSources(r.Context(), acct.ID, app.ID)
	if err != nil {
		data.ErrorMessage = "Source health is temporarily unavailable."
		return
	}
	selected := r.URL.Query().Get("source_id")
	for _, src := range data.Sources {
		if src.ID == selected {
			data.SourceID = src.ID
			sample, e := observer.ReadRealtimeInspectorOutbox(r.Context(), acct.ID, src.ID)
			if e != nil {
				data.ErrorMessage = "Outbox observations are temporarily unavailable."
				return
			}
			for _, row := range sample {
				if sameRealtimeEndpointID(row.EndpointID, data.EndpointID) && row.Channel == data.Channel {
					data.Outbox = append(data.Outbox, row)
				}
			}
			return
		}
	}
}

func sameRealtimeEndpointID(a, b string) bool {
	return strings.ReplaceAll(a, "-", "") == strings.ReplaceAll(b, "-", "")
}

func (s *Service) loadInspectorWebhooks(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	hooks, err := s.Host.Store().ListAppWebhooksForApp(r.Context(), app.ID)
	if err != nil {
		data.ErrorMessage = "Webhook data is temporarily unavailable."
		return
	}
	selected := r.URL.Query().Get("webhook_id")
	for _, hook := range hooks {
		if hook.Scope == state.AppWebhookScopeApp && hook.AccountID == acct.ID && hook.AppID == app.ID {
			data.Webhooks = append(data.Webhooks, dashboard.WebhookPageItem{ID: hook.ID, TargetURL: hook.TargetURL})
			if hook.ID == selected {
				data.WebhookID = selected
			}
		}
	}
	if data.WebhookID == "" {
		return
	}
	deliveries, _, err := s.Host.Store().ListAppWebhookDeliveries(r.Context(), app.ID, data.WebhookID, 20, "")
	if err != nil {
		data.ErrorMessage = "Webhook deliveries are temporarily unavailable."
		return
	}
	for _, delivery := range deliveries {
		if delivery.AccountID != acct.ID || delivery.AppID != app.ID || delivery.Event != state.AppWebhookEventRealtimeChannelPublished {
			continue
		}
		var event struct{ EndpointID, Channel string }
		var payload map[string]json.RawMessage
		if json.Unmarshal(delivery.Payload, &payload) != nil {
			continue
		}
		_ = json.Unmarshal(payload["endpoint_id"], &event.EndpointID)
		_ = json.Unmarshal(payload["channel"], &event.Channel)
		if !sameRealtimeEndpointID(event.EndpointID, data.EndpointID) || event.Channel != data.Channel {
			continue
		}
		data.Deliveries = append(data.Deliveries, dashboard.RealtimeInspectorDelivery{ID: delivery.ID, Status: string(delivery.Status), CreatedAt: delivery.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"), Attempts: delivery.Attempt, URL: data.WebhookURL + "?" + url.Values{"delivery_id": {delivery.ID}}.Encode()})
	}
}

func (s *Service) loadInspectorGroups(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupStore)
	if !ok {
		return
	}
	groups, err := store.ListRealtimeConsumerGroups(r.Context(), data.EndpointID, data.Channel)
	if err != nil {
		data.GroupError = "Consumer group status is temporarily unavailable."
		return
	}
	data.Groups = groups
	data.GroupURLs = make(map[string]string, len(groups))
	for _, group := range groups {
		data.GroupURLs[group.Name] = InspectorURL(data.Slug, data.EndpointID, data.Channel, "0") + "&" + url.Values{"group": {group.Name}}.Encode()
	}
	data.GroupName = r.URL.Query().Get("group")
	if data.GroupName == "" {
		return
	}
	var selected *state.RealtimeConsumerGroup
	for i := range groups {
		if groups[i].Name == data.GroupName {
			selected = &groups[i]
		}
	}
	if selected == nil {
		data.GroupError = "Consumer group is unavailable."
		return
	}
	if _, ok := s.Host.Store().(state.RealtimeConsumerGroupDeadLetterExportStore); ok {
		data.DeadLetterExportURL = InspectorDeadLetterExportURL(data.Slug, data.EndpointID, data.Channel, data.GroupName)
	}
	s.loadInspectorGroupPolicies(r, data)
	s.loadInspectorGroupHealth(r, data, *selected)
	s.loadInspectorDeliveryHistory(r, data)
	s.loadInspectorGroupAlert(r, data)
	s.loadInspectorWorkerCredentials(r, data)
	s.loadInspectorDeadLetters(r, store, data)
}

func (s *Service) loadInspectorGroupPolicies(r *http.Request, data *dashboard.RealtimeInspectorData) {
	ctx, ep, ch, group := r.Context(), data.EndpointID, data.Channel, data.GroupName
	if policies, ok := s.Host.Store().(state.RealtimeConsumerGroupRetryPolicyStore); ok {
		policy, err := policies.ReadRealtimeConsumerGroupRetryPolicy(ctx, ep, ch, group)
		if err == nil {
			data.GroupRetryPolicy = &policy
		} else {
			data.GroupError = "Retry policy is temporarily unavailable."
		}
	}
	if policies, ok := s.Host.Store().(state.RealtimeConsumerGroupClaimRateStore); ok {
		policy, err := policies.ReadRealtimeConsumerGroupClaimRate(ctx, ep, ch, group)
		if err == nil {
			data.GroupClaimRate = &policy
		} else {
			data.GroupError = "Claim rate status is temporarily unavailable."
		}
	}
	if circuits, ok := s.Host.Store().(state.RealtimeConsumerGroupCircuitStore); ok {
		status, err := circuits.ReadRealtimeConsumerGroupCircuit(ctx, ep, ch, group)
		if err == nil {
			data.GroupCircuit = &status
		} else {
			data.GroupError = "Circuit status is temporarily unavailable."
		}
	}
	if priority, ok := s.Host.Store().(state.RealtimeConsumerGroupPriorityStore); ok {
		status, err := priority.ReadRealtimeConsumerGroupPriority(ctx, ep, ch, group)
		if err == nil {
			data.GroupPriority = &status
		} else {
			data.GroupError = "Priority backlog is temporarily unavailable."
		}
	}
}

func (s *Service) loadInspectorGroupHealth(r *http.Request, data *dashboard.RealtimeInspectorData, group state.RealtimeConsumerGroup) {
	if metrics, ok := s.Host.Store().(state.RealtimeConsumerGroupMetricsStore); ok {
		status, err := metrics.ReadRealtimeConsumerGroupMetrics(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupMetrics = &status
		} else {
			data.GroupError = "Processing metrics are temporarily unavailable."
		}
	}
	if health, ok := s.Host.Store().(state.RealtimeConsumerGroupHeartbeatStore); ok {
		status, err := health.ReadRealtimeConsumerGroupWorkerHealth(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.WorkerHealth = &status
			data.NoReadyWorkers = status.ReadyWorkers == 0 && (group.Unassigned != "0" || group.Ready > 0)
		} else {
			data.GroupError = "Worker health is temporarily unavailable."
		}
	}
}

func (s *Service) loadInspectorDeadLetters(r *http.Request, store state.RealtimeConsumerGroupStore, data *dashboard.RealtimeInspectorData) {
	letters, err := store.ListRealtimeConsumerGroupDeadLetters(r.Context(), data.EndpointID, data.Channel, data.GroupName)
	if err != nil {
		data.GroupError = "Dead-letter history is temporarily unavailable."
		return
	}
	for _, letter := range letters {
		payload := letter.Message.DataBase64
		if !letter.Message.Binary {
			if decoded, err := base64.StdEncoding.DecodeString(payload); err == nil && utf8.Valid(decoded) {
				payload = string(decoded)
			}
		}
		data.GroupDeadLetters = append(data.GroupDeadLetters, dashboard.RealtimeInspectorGroupDeadLetter{RetentionStartedAt: letter.RetentionStartedAt, ExpiresAt: letter.ExpiresAt, WarningAt: letter.WarningAt, CleanupAfter: letter.CleanupAfter, Sequence: letter.Message.Sequence, FailureCode: letter.FailureCode, Attempts: letter.Attempts, Payload: payload, URL: inspectorDeadLetterURL(data, letter.Message.Sequence)})
	}
}

func inspectorDeadLetterURL(data *dashboard.RealtimeInspectorData, sequence string) string {
	seq, err := strconv.ParseInt(sequence, 10, 64)
	if err != nil || seq < 1 {
		return ""
	}
	return InspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(seq-1, 10)) + "&" + url.Values{"group": {data.GroupName}}.Encode() + "#dead-letter-" + sequence
}

func (s *Service) loadInspectorDeliveryHistory(r *http.Request, data *dashboard.RealtimeInspectorData) {
	if data.DeliverySequence == "" {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupDeliveryHistoryStore)
	if !ok {
		data.DeliveryHistoryError = "Delivery history is unavailable."
		return
	}
	after := "0"
	if values, exists := r.URL.Query()["delivery_after_id"]; exists {
		if len(values) != 1 {
			data.DeliveryHistoryError = "Choose one history cursor."
			return
		}
		after = values[0]
	}
	history, err := store.ReadRealtimeConsumerGroupDeliveryHistory(r.Context(), data.EndpointID, data.Channel, data.GroupName, data.DeliverySequence, after, api.RealtimeInspectorSampleMax)
	if err != nil {
		data.DeliveryHistoryError = "Delivery sequence or history cursor is invalid, or history is unavailable."
		return
	}
	data.DeliveryHistory = &history
	if history.HasMore {
		values := url.Values{"endpoint_id": {data.EndpointID}, "channel": {data.Channel}, "group": {data.GroupName}, "after": {data.After}, "delivery_sequence": {data.DeliverySequence}, "delivery_after_id": {history.NextAfterID}}
		data.DeliveryHistoryNextURL = "/dashboard/apps/" + url.PathEscape(data.Slug) + "/realtime?" + values.Encode()
	}
}

func (s *Service) loadInspectorGroupAlert(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupAlertStore)
	if !ok {
		return
	}
	a, err := store.ReadRealtimeConsumerGroupAlert(r.Context(), data.EndpointID, data.Channel, data.GroupName)
	if err != nil {
		data.GroupError = "Lag alert status is temporarily unavailable."
		return
	}
	data.GroupAlert = &a
}

func credentialStatus(revokedAt *time.Time, expiresAt time.Time) string {
	if revokedAt != nil {
		return "Revoked"
	}
	if !time.Now().Before(expiresAt) {
		return "Expired"
	}
	return "Active"
}

func (s *Service) loadInspectorWorkerCredentials(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.RealtimeWorkerCredentialStore)
	if !ok {
		return
	}
	rows, err := store.ListRealtimeWorkerCredentials(r.Context(), data.EndpointID, data.Channel, data.GroupName)
	if err != nil {
		data.GroupError = "Worker credential status is temporarily unavailable."
		return
	}
	for _, row := range rows {
		data.WorkerCredentials = append(data.WorkerCredentials, dashboard.RealtimeInspectorWorkerCredential{Info: row, Status: credentialStatus(row.RevokedAt, row.ExpiresAt)})
	}
}

func (s *Service) loadInspectorPublisherCredentials(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.RealtimePublisherCredentialStore)
	if !ok {
		return
	}
	rows, err := store.ListRealtimePublisherCredentials(r.Context(), data.EndpointID)
	if err != nil {
		data.ErrorMessage = "Publisher credential status is temporarily unavailable."
		return
	}
	for _, row := range rows {
		data.PublisherCredentials = append(data.PublisherCredentials, dashboard.RealtimeInspectorPublisherCredential{Info: row, Status: credentialStatus(row.RevokedAt, row.ExpiresAt)})
	}
}

// loadInspectorInbox runs only after the selected endpoint passed account/app ownership checks.
func (s *Service) loadInspectorInbox(r *http.Request, data *dashboard.RealtimeInspectorData) {
	data.InboxAfter = "0"
	data.InboxPrincipal = r.URL.Query().Get("inbox_principal")
	if data.InboxPrincipal == "" {
		return
	}
	if api.ValidateRealtimePrincipal(data.InboxPrincipal) != nil {
		data.InboxError = "Enter a valid inbox principal."
		return
	}
	if raw := r.URL.Query().Get("inbox_after"); raw != "" {
		data.InboxAfter = raw
	}
	after, err := strconv.ParseInt(data.InboxAfter, 10, 64)
	if err != nil || after < 0 {
		data.InboxError = "Inbox after must be a non-negative sequence."
		return
	}
	store, ok := s.Host.Store().(state.ManagedRealtimeInboxStore)
	if !ok {
		data.InboxError = "Inbox inspection is unavailable."
		return
	}
	history, err := store.ReadManagedRealtimeInbox(r.Context(), data.EndpointID, data.InboxPrincipal, after, api.RealtimeInspectorSampleMax)
	if err != nil {
		data.InboxError = "Inbox history is temporarily unavailable."
		return
	}
	data.InboxOldest, data.InboxLatest = strconv.FormatInt(history.OldestSequence, 10), strconv.FormatInt(history.LatestSequence, 10)
	data.InboxGap = history.HistoryUnavailable
	last := after
	for _, message := range history.Messages {
		data.InboxMessages = append(data.InboxMessages, projectInspectorMessage(message))
		last = message.Sequence
	}
	if !history.HistoryUnavailable && last < history.LatestSequence {
		query := r.URL.Query()
		query.Set("endpoint_id", data.EndpointID)
		query.Set("inbox_after", strconv.FormatInt(last, 10))
		data.InboxNextURL = "/dashboard/apps/" + url.PathEscape(data.Slug) + "/realtime?" + query.Encode()
	}
}

// loadInspectorReplayJobs lists jobs and sets the action URLs; the dashboard
// issues the CSRF token only when ReplayJobURL is set.
func (s *Service) loadInspectorReplayJobs(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.Host.Store().(state.RealtimeReplayJobStore)
	if !ok {
		return
	}
	jobs, err := store.ListRealtimeReplayJobs(r.Context(), data.EndpointID, data.Channel, data.GroupName)
	data.ReplayJobs = jobs
	if err != nil {
		data.ReplayJobError = "Replay jobs are temporarily unavailable."
	}
	data.ReplayJobURL = "/dashboard/apps/" + url.PathEscape(data.Slug) + "/realtime/replay-jobs?" + url.Values{"endpoint_id": {data.EndpointID}, "channel": {data.Channel}, "group": {data.GroupName}}.Encode()
	data.ReplayDryRunURL = strings.Replace(data.ReplayJobURL, "/realtime/replay-jobs?", "/realtime/replay-jobs/dry-run?", 1)
}
