package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventBacklogCursorPrefix = "ebc1."

type eventBacklogCursor struct {
	Version   int                                `json:"v"`
	Kind      string                             `json:"kind"`
	AccountID string                             `json:"account_id"`
	AppID     string                             `json:"app_id"`
	Filters   api.EventBacklogFilters            `json:"filters"`
	WindowAt  time.Time                          `json:"window_at"`
	Recipient state.EventBacklogPosition         `json:"recipient"`
	Consumer  state.EventBacklogConsumerPosition `json:"consumer"`
}

func (s *server) getEventBacklog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.EventBacklogReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	query, problem := parseEventBacklog(r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if query.Filters.App != "" {
		app, ok := s.loadApp(w, r, acct, query.Filters.App)
		if !ok {
			return
		}
		query.AppID = app.ID
	}
	if err := applyEventBacklogCursors(&query, r.URL.Query(), acct.ID); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventBacklogStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event backlog store"))
		return
	}
	result, err := store.EventBacklog(ctx, acct.ID, query)
	if err != nil {
		s.log.ErrorContext(ctx, "read event backlog", "err", err)
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "event_backlog_read_timeout", "Event backlog read timed out", "Narrow the app, subscription, capacity scope or age filters and retry."))
		} else {
			api.WriteProblem(w, api.ErrCapacity("failed to read event backlog"))
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, eventBacklogResponse(result, query, acct.ID))
}

func parseEventBacklog(values url.Values) (state.EventBacklogQuery, *api.Problem) {
	q := state.EventBacklogQuery{Filters: api.EventBacklogFilters{App: strings.TrimSpace(values.Get("app")), SubscriptionID: strings.TrimSpace(values.Get("subscription_id")), State: values.Get("state"), CapacityScope: values.Get("capacity_scope")}}
	if raw := values.Get("min_age_seconds"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return q, api.ErrValidation("min_age_seconds must be a whole number of seconds")
		}
		q.Filters.MinAgeSeconds = n
	}
	if err := q.Filters.Validate(); err != nil {
		return q, api.ErrValidation(err.Error())
	}
	problem, limit := api.ParseLimit(values.Get("limit"), api.EventBacklogPageDefault, api.EventBacklogPageMax, "recipients")
	if problem != nil {
		return q, problem
	}
	q.Limit = limit
	problem, limit = api.ParseLimit(values.Get("consumer_limit"), api.EventBacklogPageDefault, api.EventBacklogPageMax, "consumers")
	q.ConsumerLimit = limit
	return q, problem
}

func decodeEventBacklogCursor(raw, kind, accountID string, q state.EventBacklogQuery) (eventBacklogCursor, error) {
	invalid := errors.New("invalid or mismatched event backlog cursor")
	if len(raw) > api.EventBacklogCursorMaxBytes || !strings.HasPrefix(raw, eventBacklogCursorPrefix) {
		return eventBacklogCursor{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventBacklogCursorPrefix))
	if err != nil {
		return eventBacklogCursor{}, invalid
	}
	var c eventBacklogCursor
	if json.Unmarshal(data, &c) != nil || c.Version != 1 || c.Kind != kind || c.AccountID != accountID || c.AppID != q.AppID || c.Filters != q.Filters || c.WindowAt.IsZero() || c.WindowAt.After(time.Now().UTC()) {
		return c, invalid
	}
	if kind == "recipient" {
		if c.Recipient.OutboxID <= 0 || c.Recipient.SubscriptionID == "" || c.Recipient.AcceptedAt.IsZero() || c.Recipient.AcceptedAt.After(c.WindowAt.Add(-time.Duration(q.Filters.MinAgeSeconds)*time.Second)) {
			return c, invalid
		}
	} else if _, err = uuid.Parse(c.Consumer.AppID); err != nil || c.Consumer.SubscriptionID == "" {
		return c, invalid
	}
	return c, nil
}

func applyEventBacklogCursors(q *state.EventBacklogQuery, values url.Values, accountID string) error {
	for _, pair := range []struct{ parameter, kind string }{{"after", "recipient"}, {"consumers_after", "consumer"}} {
		raw := values.Get(pair.parameter)
		if raw == "" {
			continue
		}
		c, err := decodeEventBacklogCursor(raw, pair.kind, accountID, *q)
		if err != nil {
			return err
		}
		if !q.WindowAt.IsZero() && !q.WindowAt.Equal(c.WindowAt) {
			return errors.New("event backlog cursors must use the same window")
		}
		q.WindowAt = c.WindowAt
		if pair.kind == "recipient" {
			q.After = c.Recipient
		} else {
			q.ConsumersAfter = c.Consumer
		}
	}
	if q.WindowAt.IsZero() {
		q.WindowAt = time.Now().UTC()
	}
	return nil
}

func encodeEventBacklogCursor(kind, accountID string, q state.EventBacklogQuery, recipient state.EventBacklogPosition, consumer state.EventBacklogConsumerPosition) string {
	if kind == "recipient" && recipient.OutboxID == 0 || kind == "consumer" && consumer.AppID == "" {
		return ""
	}
	data, _ := json.Marshal(eventBacklogCursor{Version: 1, Kind: kind, AccountID: accountID, AppID: q.AppID, Filters: q.Filters, WindowAt: q.WindowAt, Recipient: recipient, Consumer: consumer})
	return eventBacklogCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func eventBacklogResponse(result state.EventBacklog, q state.EventBacklogQuery, accountID string) api.EventBacklogResponse {
	out := api.EventBacklogResponse{ObservedAt: result.ObservedAt, WindowAt: result.WindowAt, Coverage: api.EventBacklogCoverage, Recipients: []api.EventBacklogRecipient{}, Consumers: result.Consumers, UnattributedReceipts: result.UnattributedReceipts,
		NextAfter:          encodeEventBacklogCursor("recipient", accountID, q, result.Next, state.EventBacklogConsumerPosition{}),
		NextConsumersAfter: encodeEventBacklogCursor("consumer", accountID, q, state.EventBacklogPosition{}, result.NextConsumer)}
	for _, entry := range result.Recipients {
		r := entry.EventBacklogRecipient
		r.ReceiptURL = eventReceiptURL(r.EventSource, r.EventID)
		if r.AppSlug != "" {
			query := url.Values{"event_source": {r.EventSource}, "event_id": {r.EventID}, "subscription_id": {r.SubscriptionID}}
			r.FanoutHistoryURL = "/v1/apps/" + url.PathEscape(r.AppSlug) + "/event-deliveries/attempts?" + query.Encode()
		}
		out.Recipients = append(out.Recipients, r)
	}
	return out
}
