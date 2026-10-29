// Package wakehistory implements opt-in retained instance-history paging (ADR-1638).
package wakehistory

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func ReadPage(ctx context.Context, store any, account string, app state.App, q url.Values) (api.AppWakeTimelineResponse, *api.Problem) {
	fail := api.ErrValidation("invalid wake history page selection")
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := cursor{Version: 1, Account: account, App: app.ID, Limit: api.WakeHistoryPageDefault}
	if q.Get("pagination") != "keyset_v1" || len(q.Get("cursor")) > api.WakeHistoryCursorMaxBytes {
		return api.AppWakeTimelineResponse{}, fail
	}
	until := now
	if raw := q.Get("until"); raw != "" {
		var err error
		until, err = parseTime(raw)
		if err != nil || until.After(now) {
			return api.AppWakeTimelineResponse{}, fail
		}
	}
	since := until.Add(-24 * time.Hour)
	if raw := q.Get("since"); raw != "" {
		var err error
		since, err = parseTime(raw)
		if err != nil {
			return api.AppWakeTimelineResponse{}, fail
		}
	}
	if !since.Before(until) {
		return api.AppWakeTimelineResponse{}, fail
	}
	c.Since, c.Until = since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano)
	if raw := q.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > api.WakeHistoryPageMax {
			return api.AppWakeTimelineResponse{}, fail
		}
		c.Limit = n
	}
	var after state.WakeHistoryPosition
	if raw := q.Get("cursor"); raw != "" {
		var err error
		after, err = decode(raw, c)
		if err != nil || after.At.Before(since) || after.At.After(until) {
			return api.AppWakeTimelineResponse{}, fail
		}
	}
	s, ok := store.(state.WakeHistoryPageStore)
	if !ok {
		return api.AppWakeTimelineResponse{}, api.ErrCapacity("paged wake history unavailable")
	}
	rows, err := s.ListAppWakeHistoryPage(ctx, app.ID, since, until, after, c.Limit+1)
	if err != nil {
		return api.AppWakeTimelineResponse{}, api.ErrCapacity("could not read paged wake history")
	}
	return build(app, c, q.Get("cursor"), since, until, after, rows)
}

func build(app state.App, c cursor, raw string, since, until time.Time, after state.WakeHistoryPosition, rows []state.WakeHistoryRecord) (api.AppWakeTimelineResponse, *api.Problem) {
	fail := api.ErrCapacity("invalid paged wake history source")
	out := api.AppWakeTimelineResponse{App: api.WakeTimelineApp{AppID: app.ID, Slug: app.Slug}, Rows: []api.WakeTimelineJSONRow{}, TriggerHistogram: map[string]int{}, TriggerClassHistogram: map[string]int{}, AsOf: time.Now().UTC().Format(time.RFC3339Nano), Page: &api.AppWakeHistoryPage{Mode: "keyset_v1", Since: c.Since, Until: c.Until, Limit: c.Limit, Cursor: raw, Complete: len(rows) <= c.Limit, SourceStatus: "read_succeeded", CountScope: "returned_page"}}
	if len(rows) > c.Limit+1 {
		return out, fail
	}
	previous := after
	for i, r := range rows {
		id, e := uuid.Parse(r.InstanceID)
		if e != nil || id.String() != r.InstanceID || r.AppID != app.ID || r.StartedAt.IsZero() || !r.StartedAt.Equal(r.StartedAt.Truncate(time.Microsecond)) || r.StartedAt.Before(since) || r.StartedAt.After(until) || previous.ID != "" && (r.StartedAt.After(previous.At) || r.StartedAt.Equal(previous.At) && r.InstanceID >= previous.ID) {
			return out, fail
		}
		previous = state.WakeHistoryPosition{At: r.StartedAt, ID: r.InstanceID}
		row, err := project(r)
		if err != nil {
			return out, fail
		}
		if i >= c.Limit {
			continue
		}
		out.Rows = append(out.Rows, row)
		out.WakeCount24h++
		if r.HasMeta {
			out.WakeCountWithMeta++
		}
		if row.AtCapacityPresent && row.AtCapacity {
			out.AtCapacityCount++
		}
		if row.Trigger != "" {
			out.TriggerHistogram[row.Trigger]++
		}
		if row.TriggerClass != "" {
			out.TriggerClassHistogram[row.TriggerClass]++
		}
	}
	if out.WakeCountWithMeta > 0 {
		out.AtCapacityPct = float64(out.AtCapacityCount) / float64(out.WakeCountWithMeta) * 100
	}
	if !out.Page.Complete {
		var err error
		out.Page.NextCursor, err = encode(c, rows[c.Limit-1])
		if err != nil {
			return out, fail
		}
	}
	return out, nil
}

func project(r state.WakeHistoryRecord) (api.WakeTimelineJSONRow, error) {
	out := api.WakeTimelineJSONRow{WakeID: r.WakeID, State: r.State, Kind: "wake.boot_started", At: r.StartedAt.UTC().Format(time.RFC3339Nano), ReadyInMS: -1}
	if !r.HasMeta {
		return out, nil
	}
	var p struct {
		AppID        string `json:"app_id"`
		WakeID       string `json:"wake_id"`
		Trigger      string `json:"trigger"`
		TriggerClass string `json:"trigger_class"`
		Method       string `json:"method"`
		Tier         string `json:"tier"`
		Queued       int32  `json:"queued_count"`
		Concurrency  int32  `json:"concurrency_at_admit"`
		Capacity     *bool  `json:"at_capacity"`
	}
	err := json.Unmarshal(r.BootData, &p)
	if err != nil {
		return out, err
	}
	if p.AppID != r.AppID || p.WakeID != r.WakeID || p.Queued < 0 || p.Concurrency < 0 || r.BootAt.IsZero() {
		return out, errSource
	}
	out.Trigger, out.TriggerClass, out.Method, out.Tier = p.Trigger, p.TriggerClass, p.Method, p.Tier
	out.QueuedCount, out.ConcurrencyAtAdmit = p.Queued, p.Concurrency
	applyCompletion(&out, r.CompletedData)
	if p.Capacity != nil {
		out.AtCapacityPresent = true
		out.AtCapacity = *p.Capacity
	}
	if r.CompletedAt.After(r.BootAt) {
		ms := r.CompletedAt.Sub(r.BootAt).Milliseconds()
		if ms > math.MaxInt32 {
			return out, errSource
		}
		if ms > 0 {
			out.ReadyInMS = int32(ms)
		}
	}
	return out, nil
}

// applyCompletion reports the method vmmd actually ran. wake.boot_started
// names the planned method; when a planned restore fell back to a cold boot
// (ADR-005), only wake.boot_completed says so (Hunt 12 H12-49). The planned
// method stays visible, and the tier becomes the cold-boot fallback it was.
func applyCompletion(out *api.WakeTimelineJSONRow, data json.RawMessage) {
	var c struct {
		Method         string `json:"method"`
		FallbackReason string `json:"restore_fallback_reason"`
	}
	if len(data) == 0 || json.Unmarshal(data, &c) != nil || c.Method == "" || c.Method == out.Method {
		return
	}
	out.PlannedMethod, out.Method = out.Method, c.Method
	if out.PlannedMethod == "restore" && c.Method == "cold_boot" {
		out.Tier = "cold_boot_fallback"
		// The closed customer-facing reason; vmmd's raw restore error never
		// reaches a customer.
		if c.FallbackReason == "after_restore_failed" {
			out.RestoreFallbackReason = c.FallbackReason
		}
	}
}
