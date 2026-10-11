package apidserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppRealtimePath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	if !strings.HasSuffix(rest, "/realtime") {
		return "", false
	}
	slug := strings.TrimSuffix(rest, "/realtime")
	return slug, slug != "" && !strings.Contains(slug, "/")
}
func inspectorURL(slug, endpoint, channel, after string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/realtime?" + url.Values{"endpoint_id": {endpoint}, "channel": {channel}, "after": {after}}.Encode()
}
func (s *server) renderAppRealtime(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	_, allowed := s.realtime.RealtimePlanLimits(acct)
	data := dashboard.RealtimeInspectorData{Slug: slug, Enabled: allowed && s.realtime.HistoryPreviewEnabled, After: "0", WebhookURL: "/dashboard/apps/" + url.PathEscape(slug) + "/webhooks"}
	if data.Enabled {
		s.loadRealtimeInspector(r, acct, app, &data)
		if data.EndpointID != "" {
			s.loadRealtimeInspectorInbox(r, &data)
		}
	}

	if data.GroupName != "" && data.GroupError == "" {
		if store, ok := s.store.(state.RealtimeReplayJobStore); ok {
			jobs, err := store.ListRealtimeReplayJobs(r.Context(), data.EndpointID, data.Channel, data.GroupName)
			data.ReplayJobs = jobs
			if err != nil {
				data.ReplayJobError = "Replay jobs are temporarily unavailable."
			}
			data.ReplayJobURL = "/dashboard/apps/" + url.PathEscape(slug) + "/realtime/replay-jobs?" + url.Values{"endpoint_id": {data.EndpointID}, "channel": {data.Channel}, "group": {data.GroupName}}.Encode()
			data.ReplayDryRunURL = strings.Replace(data.ReplayJobURL, "/realtime/replay-jobs?", "/realtime/replay-jobs/dry-run?", 1)
			token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, "realtime_replay_job", acct.ID, "faas_replay_job_csrf")
			if tokenErr == nil {
				data.ReplayJobCSRF = token
				http.SetCookie(w, &http.Cookie{Name: "faas_replay_job_csrf", Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
			}
		}
	}
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	view, _ := AccountFrom(r.Context())
	page := dashboard.Page{Title: "Realtime — " + slug, Body: "realtime_inspector", Account: dashboardAccountView(view, count), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
func (s *server) loadRealtimeInspector(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	endpoints, ok := realtimeEndpointStore(s)
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
	s.loadRealtimeInspectorPublisherCredentials(r, data)
	directory, ok := s.store.(state.RealtimeInspectorStore)
	if !ok {
		data.ErrorMessage = "Channel listing is unavailable."
		return
	}
	data.Channels, err = directory.ListRealtimeInspectorChannels(r.Context(), selected)
	if err != nil {
		data.ErrorMessage = "Channel listing is temporarily unavailable."
		return
	}
	data.DeliverySequence = r.URL.Query().Get("delivery_sequence")
	data.Channel = r.URL.Query().Get("channel")
	if data.Channel == "" && len(data.Channels) > 0 {
		data.Channel = data.Channels[0].Channel
	}
	if data.Channel != "" {
		s.loadRealtimeInspectorSchedules(r, data)
		s.loadRealtimeInspectorHistory(r, data)
		s.loadRealtimeInspectorGroups(r, data)
	}
	s.loadRealtimeInspectorSources(r, acct, app, data)
	s.loadRealtimeInspectorWebhooks(r, acct, app, data)
}
func (s *server) loadRealtimeInspectorHistory(r *http.Request, data *dashboard.RealtimeInspectorData) {
	history, ok := s.store.(state.ManagedRealtimeHistoryStore)
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
	floor := page.OldestSequence - 1
	if floor < 0 {
		floor = 0
	}
	data.BrowseURL = inspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(floor, 10))
	if data.Gap {
		return
	}
	for _, message := range page.Messages {
		data.Messages = append(data.Messages, projectRealtimeInspectorMessage(message))
	}
	if len(page.Messages) > 0 {
		last := page.Messages[len(page.Messages)-1].Sequence
		if last < page.LatestSequence {
			data.NextURL = inspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(last, 10))
		}
	}
}
func projectRealtimeInspectorMessage(message state.ManagedRealtimeChannelMessage) dashboard.RealtimeInspectorMessage {
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
func (s *server) loadRealtimeInspectorSources(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	observer, ok := s.store.(state.RealtimeInspectorOutboxStore)
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
				if strings.ReplaceAll(row.EndpointID, "-", "") == strings.ReplaceAll(data.EndpointID, "-", "") && row.Channel == data.Channel {
					data.Outbox = append(data.Outbox, row)
				}
			}
			return
		}
	}
}
func (s *server) loadRealtimeInspectorWebhooks(r *http.Request, acct state.Account, app state.App, data *dashboard.RealtimeInspectorData) {
	hooks, err := s.store.ListAppWebhooksForApp(r.Context(), app.ID)
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
	deliveries, _, err := s.store.ListAppWebhookDeliveries(r.Context(), app.ID, data.WebhookID, 20, "")
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
		if strings.ReplaceAll(event.EndpointID, "-", "") != strings.ReplaceAll(data.EndpointID, "-", "") || event.Channel != data.Channel {
			continue
		}
		data.Deliveries = append(data.Deliveries, dashboard.RealtimeInspectorDelivery{ID: delivery.ID, Status: string(delivery.Status), CreatedAt: delivery.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"), Attempts: delivery.Attempt, URL: data.WebhookURL + "?" + url.Values{"delivery_id": {delivery.ID}}.Encode()})
	}
}

func (s *server) loadRealtimeInspectorGroups(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.store.(state.RealtimeConsumerGroupStore)
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
		data.GroupURLs[group.Name] = inspectorURL(data.Slug, data.EndpointID, data.Channel, "0") + "&" + url.Values{"group": {group.Name}}.Encode()
	}
	data.GroupName = r.URL.Query().Get("group")
	if data.GroupName == "" {
		return
	}
	found := false
	for _, g := range groups {
		if g.Name == data.GroupName {
			found = true
		}
	}
	if !found {
		data.GroupError = "Consumer group is unavailable."
		return
	}
	if _, ok := s.store.(state.RealtimeConsumerGroupDeadLetterExportStore); ok {
		data.DeadLetterExportURL = realtimeInspectorDeadLetterExportURL(data.Slug, data.EndpointID, data.Channel, data.GroupName)
	}
	if policies, ok := s.store.(state.RealtimeConsumerGroupRetryPolicyStore); ok {
		policy, err := policies.ReadRealtimeConsumerGroupRetryPolicy(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupRetryPolicy = &policy
		} else {
			data.GroupError = "Retry policy is temporarily unavailable."
		}
	}
	if policies, ok := s.store.(state.RealtimeConsumerGroupClaimRateStore); ok {
		policy, err := policies.ReadRealtimeConsumerGroupClaimRate(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupClaimRate = &policy
		} else {
			data.GroupError = "Claim rate status is temporarily unavailable."
		}
	}
	if circuits, ok := s.store.(state.RealtimeConsumerGroupCircuitStore); ok {
		status, err := circuits.ReadRealtimeConsumerGroupCircuit(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupCircuit = &status
		} else {
			data.GroupError = "Circuit status is temporarily unavailable."
		}
	}
	if priority, ok := s.store.(state.RealtimeConsumerGroupPriorityStore); ok {
		status, err := priority.ReadRealtimeConsumerGroupPriority(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupPriority = &status
		} else {
			data.GroupError = "Priority backlog is temporarily unavailable."
		}
	}
	if metrics, ok := s.store.(state.RealtimeConsumerGroupMetricsStore); ok {
		status, err := metrics.ReadRealtimeConsumerGroupMetrics(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.GroupMetrics = &status
		} else {
			data.GroupError = "Processing metrics are temporarily unavailable."
		}
	}
	if health, ok := s.store.(state.RealtimeConsumerGroupHeartbeatStore); ok {
		status, err := health.ReadRealtimeConsumerGroupWorkerHealth(r.Context(), data.EndpointID, data.Channel, data.GroupName)
		if err == nil {
			data.WorkerHealth = &status
			for _, g := range groups {
				if g.Name == data.GroupName {
					data.NoReadyWorkers = status.ReadyWorkers == 0 && (g.Unassigned != "0" || g.Ready > 0)
				}
			}
		} else {
			data.GroupError = "Worker health is temporarily unavailable."
		}
	}
	s.loadRealtimeInspectorDeliveryHistory(r, data)
	s.loadRealtimeInspectorGroupAlert(r, data)
	s.loadRealtimeInspectorWorkerCredentials(r, data)
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
		data.GroupDeadLetters = append(data.GroupDeadLetters, dashboard.RealtimeInspectorGroupDeadLetter{RetentionStartedAt: letter.RetentionStartedAt, ExpiresAt: letter.ExpiresAt, WarningAt: letter.WarningAt, CleanupAfter: letter.CleanupAfter, Sequence: letter.Message.Sequence, FailureCode: letter.FailureCode, Attempts: letter.Attempts, Payload: payload, URL: realtimeDeadLetterURL(data, letter.Message.Sequence)})
	}
}

func (s *server) loadRealtimeInspectorWorkerCredentials(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.store.(state.RealtimeWorkerCredentialStore)
	if !ok {
		return
	}
	rows, err := store.ListRealtimeWorkerCredentials(r.Context(), data.EndpointID, data.Channel, data.GroupName)
	if err != nil {
		data.GroupError = "Worker credential status is temporarily unavailable."
		return
	}
	for _, row := range rows {
		status := "Active"
		if row.RevokedAt != nil {
			status = "Revoked"
		} else if !time.Now().Before(row.ExpiresAt) {
			status = "Expired"
		}
		data.WorkerCredentials = append(data.WorkerCredentials, dashboard.RealtimeInspectorWorkerCredential{Info: row, Status: status})
	}
}

func (s *server) loadRealtimeInspectorPublisherCredentials(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.store.(state.RealtimePublisherCredentialStore)
	if !ok {
		return
	}
	rows, err := store.ListRealtimePublisherCredentials(r.Context(), data.EndpointID)
	if err != nil {
		data.ErrorMessage = "Publisher credential status is temporarily unavailable."
		return
	}
	for _, row := range rows {
		status := "Active"
		if row.RevokedAt != nil {
			status = "Revoked"
		} else if !time.Now().Before(row.ExpiresAt) {
			status = "Expired"
		}
		data.PublisherCredentials = append(data.PublisherCredentials, dashboard.RealtimeInspectorPublisherCredential{Info: row, Status: status})
	}
}

func realtimeDeadLetterURL(data *dashboard.RealtimeInspectorData, sequence string) string {
	seq, err := strconv.ParseInt(sequence, 10, 64)
	if err != nil || seq < 1 {
		return ""
	}
	return inspectorURL(data.Slug, data.EndpointID, data.Channel, strconv.FormatInt(seq-1, 10)) + "&" + url.Values{"group": {data.GroupName}}.Encode() + "#dead-letter-" + sequence
}

func (s *server) loadRealtimeInspectorGroupAlert(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.store.(state.RealtimeConsumerGroupAlertStore)
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

// loadRealtimeInspectorInbox runs only after the selected endpoint passed account/app ownership checks.
func (s *server) loadRealtimeInspectorInbox(r *http.Request, data *dashboard.RealtimeInspectorData) {
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
	store, ok := s.store.(state.ManagedRealtimeInboxStore)
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
		data.InboxMessages = append(data.InboxMessages, projectRealtimeInspectorMessage(message))
		last = message.Sequence
	}
	if !history.HistoryUnavailable && last < history.LatestSequence {
		query := r.URL.Query()
		query.Set("endpoint_id", data.EndpointID)
		query.Set("inbox_after", strconv.FormatInt(last, 10))
		data.InboxNextURL = "/dashboard/apps/" + url.PathEscape(data.Slug) + "/realtime?" + query.Encode()
	}
}
