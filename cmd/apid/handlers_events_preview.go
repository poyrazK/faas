package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	eventPreviewSubscriptionBatch = 256
	eventPreviewSampleLimit       = 100
)

// previewEvent evaluates an event against the same bounded subscription
// lookup and matcher used by schedd, but deliberately does not append the
// event or wake the fanout worker.
func (s *server) previewEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PreviewEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}

	eventID := strings.TrimSpace(req.ID)
	if eventID == "" {
		eventID = uuid.NewString()
	}
	var occurredAt time.Time
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (events.Envelope{
		ID:              eventID,
		Source:          req.Source,
		Type:            req.Type,
		Time:            occurredAt,
		DataContentType: req.DataContentType,
		Data:            req.Data,
	}).Normalize(acct.ID, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	matcher, ok := s.store.(state.EventSubscriptionMatcherStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event preview"))
		return
	}
	out := api.PreviewEventResponse{
		EventID:    envelope.ID,
		Source:     envelope.Source,
		Type:       envelope.Type,
		Matches:    make([]api.EventPreviewSubscription, 0),
		NonMatches: make([]api.EventPreviewSubscription, 0),
	}
	var appSlugs map[string]string
	cursor := state.EventSubscriptionCursor{}
	for {
		subscriptions, listErr := matcher.ListMatchingEventSubscriptionsForAccount(
			r.Context(), acct.ID, envelope.Source, envelope.Type, cursor, eventPreviewSubscriptionBatch,
		)
		if listErr != nil {
			s.log.ErrorContext(r.Context(), "event preview subscription lookup failed", "account_id", acct.ID, "err", listErr)
			api.WriteProblem(w, api.ErrInternal("event preview"))
			return
		}
		if len(subscriptions) == 0 {
			break
		}
		if appSlugs == nil {
			apps, appsErr := s.store.ListApps(r.Context(), acct.ID)
			if appsErr != nil {
				s.log.ErrorContext(r.Context(), "event preview app lookup failed", "account_id", acct.ID, "err", appsErr)
				api.WriteProblem(w, api.ErrInternal("event preview"))
				return
			}
			appSlugs = make(map[string]string, len(apps))
			for _, app := range apps {
				appSlugs[canonicalEventPreviewUUID(app.ID)] = app.Slug
			}
		}

		for _, row := range subscriptions {
			out.CandidateCount++
			item := api.EventPreviewSubscription{
				AppSlug:        appSlugs[canonicalEventPreviewUUID(row.AppID)],
				SubscriptionID: row.ID,
				Source:         row.Source,
				Type:           row.Type,
				Filter:         append(json.RawMessage(nil), row.Filter...),
			}
			reason, matchErr := (events.Subscription{
				ID:        row.ID,
				AccountID: row.AccountID,
				Source:    row.Source,
				Type:      row.Type,
				Filter:    row.Filter,
			}).ExplainMatch(envelope)
			if matchErr != nil {
				out.OtherMismatchCount++
				item.Reason = "invalid_subscription: " + matchErr.Error()
				appendEventPreviewSample(&out.NonMatches, item, &out.Truncated)
				continue
			}
			item.Reason = string(reason)
			switch reason {
			case events.MatchReasonWouldDeliver:
				out.MatchedCount++
				appendEventPreviewSample(&out.Matches, item, &out.Truncated)
			case events.MatchReasonFilterMismatch:
				out.FilterMismatchCount++
				appendEventPreviewSample(&out.NonMatches, item, &out.Truncated)
			default:
				out.OtherMismatchCount++
				appendEventPreviewSample(&out.NonMatches, item, &out.Truncated)
			}
		}
		if len(subscriptions) < eventPreviewSubscriptionBatch {
			break
		}
		last := subscriptions[len(subscriptions)-1]
		cursor = state.EventSubscriptionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	writeJSON(w, http.StatusOK, out)
}

func canonicalEventPreviewUUID(value string) string {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return value
	}
	return parsed.String()
}

func appendEventPreviewSample(dst *[]api.EventPreviewSubscription, item api.EventPreviewSubscription, truncated *bool) {
	if len(*dst) >= eventPreviewSampleLimit {
		*truncated = true
		return
	}
	*dst = append(*dst, item)
}
