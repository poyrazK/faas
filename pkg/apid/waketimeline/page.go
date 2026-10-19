package waketimeline

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type selection struct {
	since  string
	from   time.Time
	limit  int
	cursor string
}

func parse(q url.Values) (selection, *api.Problem) {
	p := selection{limit: api.WakeTimelinePageDefault, cursor: q.Get("cursor")}
	fail := api.ErrValidation("invalid keyset wake timeline selection")
	if q.Get("pagination") != "keyset_v1" || len(p.cursor) > api.WakeTimelineCursorMaxBytes {
		return p, fail
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.WakeTimelinePageMax {
			return p, fail
		}
		p.limit = n
	}
	if raw := q.Get("since"); raw != "" {
		if len(raw) > api.WakeTimelineTimeMaxBytes {
			return p, fail
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		t = t.UTC().Truncate(time.Microsecond)
		if err != nil || t.IsZero() {
			return p, fail
		}
		p.from, p.since = t, t.Format(time.RFC3339Nano)
	}
	return p, nil
}

// ReadPage implements the opt-in keyset contract. The cursor is selection data,
// never authorization; the caller resolves ownership and plan/MFA on every read.
func ReadPage(ctx context.Context, store any, accountID, appID, wakeID string, q url.Values) (api.WakeTimelineResponse, *api.Problem) {
	p, problem := parse(q)
	if problem != nil {
		return api.WakeTimelineResponse{}, problem
	}
	if strings.TrimSpace(wakeID) == "" || len(wakeID) > api.WakeTimelineIDMaxBytes || !utf8.ValidString(wakeID) || strings.ContainsFunc(wakeID, unicode.IsControl) {
		return api.WakeTimelineResponse{}, api.ErrValidation("invalid wake ID")
	}
	want := cursorContext(accountID, appID, wakeID, p)
	var after state.WakeTimelinePosition
	if p.cursor != "" {
		var err error
		after, err = decodeCursor(p.cursor, want)
		if err != nil || !after.At.After(p.from) {
			return api.WakeTimelineResponse{}, api.ErrValidation("wake timeline cursor does not match the app, wake or selection")
		}
	}
	s, ok := store.(state.WakeTimelinePageStore)
	if !ok {
		return api.WakeTimelineResponse{}, api.ErrCapacity("keyset wake timelines unavailable")
	}
	rows, err := s.ListAppWakeTimelinePage(ctx, appID, wakeID, p.from, after, p.limit+1)
	if err != nil {
		return api.WakeTimelineResponse{}, api.ErrCapacity("could not list keyset wake timeline")
	}
	if len(rows) == 0 && p.cursor == "" && p.since == "" {
		return api.WakeTimelineResponse{}, api.NewProblem(404, api.CodeNotFound, "Wake not found", "no retained wake events belong to this app")
	}
	return buildPage(appID, wakeID, p, want, after, rows)
}

func buildPage(appID, wakeID string, p selection, c pageCursor, after state.WakeTimelinePosition, rows []state.Event) (api.WakeTimelineResponse, *api.Problem) {
	fail := api.ErrCapacity("invalid keyset wake timeline source")
	if len(rows) > p.limit+1 {
		return api.WakeTimelineResponse{}, fail
	}
	out := api.WakeTimelineResponse{AppID: appID, WakeID: wakeID, Limit: p.limit, Events: []api.WakeTimelineEvent{}, Page: &api.WakeTimelinePage{Mode: "keyset_v1", Since: p.since, Cursor: p.cursor, Complete: len(rows) <= p.limit}}
	previous := after
	for i, e := range rows {
		var data map[string]any
		if json.Unmarshal(e.Data, &data) != nil || data["app_id"] != appID || data["wake_id"] != wakeID || e.ID < 1 || e.At.IsZero() || !e.At.Equal(e.At.Truncate(time.Microsecond)) || !e.At.After(p.from) || previous.ID > 0 && (e.At.Before(previous.At) || e.At.Equal(previous.At) && e.ID <= previous.ID) {
			return api.WakeTimelineResponse{}, fail
		}
		previous = state.WakeTimelinePosition{At: e.At, ID: e.ID}
		if i < p.limit {
			out.Events = append(out.Events, api.WakeTimelineEvent{At: e.At.UTC().Format(time.RFC3339Nano), Kind: e.Kind, Actor: e.Actor, Data: data})
		}
	}
	if !out.Page.Complete {
		raw, err := encodeCursor(c, rows[p.limit-1])
		if err != nil {
			return api.WakeTimelineResponse{}, fail
		}
		out.Page.NextCursor = raw
	}
	return out, nil
}
