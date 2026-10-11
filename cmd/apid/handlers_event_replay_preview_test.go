package main

// adr: 624

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func replayPreviewAPIPath(app, sub string, from, until time.Time) string {
	return "/v1/apps/" + app + "/event-subscriptions/" + sub + "/replay-preview?" + url.Values{"from": {from.Format(time.RFC3339Nano)}, "until": {until.Format(time.RFC3339Nano)}}.Encode()
}

func TestEventReplayPreviewAPIReadOnlyMetadataAndContinuation(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "preview-history")
	from, until := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, id := range []string{"old-1", "old-2"} {
		rec := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: id, Source: "orders", Type: "created", Data: json.RawMessage(`{"private":"retained-payload-secret"}`)}, nil)
		if rec.Code != 202 {
			t.Fatalf("publish=%d %s", rec.Code, rec.Body)
		}
	}
	sub, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, app, "orders", "created", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := replayPreviewAPIPath("preview-history", sub.ID, from, until)
	rec := e.do(t, http.MethodGet, path+"&limit=1", nil, nil)
	var first api.EventReplayPreviewResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &first) != nil || rec.Header().Get("Cache-Control") != "no-store" || first.ScannedCount != 1 || first.MatchedCount != 1 || len(first.Matches) != 1 || first.NextAfter == "" || first.Matches[0].OriginalRecipient != "not_captured" || strings.Contains(rec.Body.String(), "retained-payload-secret") {
		t.Fatalf("preview=%d %s", rec.Code, rec.Body)
	}
	link, err := url.Parse(first.Matches[0].ReceiptURL)
	if err != nil || link.Path != "/v1/events/receipt" || link.Query().Get("source") != "orders" || link.Query().Get("id") != "old-1" {
		t.Fatalf("receipt link=%v %v", link, err)
	}
	rec = e.do(t, http.MethodGet, path+"&after="+url.QueryEscape(first.NextAfter), nil, nil)
	var second api.EventReplayPreviewResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &second) != nil || second.MatchedCount != 1 || second.Matches[0].EventID != "old-2" || second.NextAfter != "" || !second.CutoffAt.Equal(first.CutoffAt) {
		t.Fatalf("continuation=%d %s", rec.Code, rec.Body)
	}
	rows, err := e.store.ListInvocationsForAccount(context.Background(), e.acct.ID, 100, "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("preview invoked handler=%+v %v", rows, err)
	}
	for _, suffix := range []string{"&limit=0", "&limit=101", "&after=bad", "&after=" + strings.Repeat("x", api.EventReplayPreviewCursorMaxBytes+1)} {
		rec := e.do(t, http.MethodGet, path+suffix, nil, nil)
		if rec.Code != 400 {
			t.Fatalf("invalid=%d %s", rec.Code, rec.Body)
		}
	}
	for _, bad := range []string{replayPreviewAPIPath("preview-history", sub.ID, until, from), "/v1/apps/preview-history/event-subscriptions/" + sub.ID + "/replay-preview?from=bad&until=bad"} {
		if rec := e.do(t, http.MethodGet, bad, nil, nil); rec.Code != 400 {
			t.Fatalf("range=%d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, http.MethodGet, replayPreviewAPIPath("preview-history", uuid.NewString(), from, until), nil, nil); rec.Code != 404 {
		t.Fatalf("unknown subscription=%d %s", rec.Code, rec.Body)
	}
	foreignApp := mustSeedApp(t, e, "preview-other-app")
	other, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, foreignApp, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodGet, replayPreviewAPIPath("preview-history", other.ID, from, until), nil, nil); rec.Code != 404 {
		t.Fatalf("wrong app=%d %s", rec.Code, rec.Body)
	}
}

func TestEventReplayPreviewAPIReadScopes(t *testing.T) {
	for _, scope := range []string{api.ScopeAppsRead, api.ScopeEventsPublish} {
		t.Run(scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{scope})
			app := mustSeedApp(t, e, "preview-scope")
			sub, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, app, "*", "*", nil)
			if err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, http.MethodGet, replayPreviewAPIPath("preview-scope", sub.ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour)), nil, nil)
			want := 200
			if scope == api.ScopeEventsPublish {
				want = 403
			}
			if rec.Code != want {
				t.Fatalf("scope=%s status=%d %s", scope, rec.Code, rec.Body)
			}
		})
	}
}

type failedReplayPreviewStore struct {
	state.Store
	err error
}

func (s failedReplayPreviewStore) PreviewEventReplay(context.Context, string, state.EventReplayPreviewQuery) (api.EventReplayPreviewResponse, error) {
	return api.EventReplayPreviewResponse{}, s.err
}

func TestEventReplayPreviewAPIConflictsAndTimeout(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{{state.ErrEventReplayPreviewChanged, 409, "event_replay_preview_changed"}, {state.ErrEventReplayPreviewDisabled, 409, "event_replay_preview_disabled"}, {state.ErrEventReplayPreviewUnsupported, 409, "event_replay_preview_unsupported"}, {context.DeadlineExceeded, 503, "event_replay_preview_read_timeout"}} {
		t.Run(test.code, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			mustSeedApp(t, e, "preview-error")
			e.s.store = failedReplayPreviewStore{Store: e.store, err: test.err}
			rec := e.do(t, http.MethodGet, replayPreviewAPIPath("preview-error", uuid.NewString(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour)), nil, nil)
			var problem api.Problem
			if rec.Code != test.status || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != test.code || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("error=%d %s", rec.Code, rec.Body)
			}
		})
	}
}
