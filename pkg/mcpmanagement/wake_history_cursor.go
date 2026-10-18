package mcpmanagement

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type historyCursor struct {
	Version int    `json:"v"`
	App     string `json:"app"`
	AppID   string `json:"app_id"`
	Since   string `json:"since"`
	Until   string `json:"until"`
	Limit   int    `json:"limit"`
	Native  string `json:"native"`
	LastAt  string `json:"last_at"`
}

func (m *management) readHistoryCursor(args wakeHistoryArgs) (historyCursor, error) {
	c := historyCursor{Version: 1, App: args.App, Since: args.Since, Until: args.Until, Limit: args.Limit}
	if args.Cursor == "" {
		return c, nil
	}
	fail := invalid("invalid_argument", "wake history cursor must preserve app, original since/until and limit")
	s, ok := strings.CutPrefix(args.Cursor, "hp1.")
	if !ok || !m.safeWakeCursor(args.Cursor, api.MCPWakeHistoryCursorMaxBytes) {
		return c, fail
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil {
		return c, fail
	}
	var got historyCursor
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&got) != nil || d.Decode(new(any)) != io.EOF || got.Version != c.Version || got.App != c.App || got.Since != c.Since || got.Until != c.Until || got.Limit != c.Limit || validateSegment(got.AppID, "app_id") != nil || len(got.AppID) > api.MCPRequestIdentifierMaxBytes || m.clean(got.AppID) != got.AppID || !m.safeWakeCursor(got.Native, api.WakeHistoryCursorMaxBytes) {
		return c, fail
	}
	at, ok := wakeHistoryTime(got.LastAt)
	if !ok || at.IsZero() {
		return c, fail
	}
	return got, nil
}
func (m *management) projectHistoryPagination(args wakeHistoryArgs, appID string, n api.AppWakeTimelineResponse, v *wakeHistoryView) error {
	if n.Page == nil {
		if args.Cursor != "" {
			return invalid("native_upgrade_required", "native API no longer supplies paged wake history")
		}
		return nil
	}
	c, err := m.readHistoryCursor(args)
	if err != nil {
		return err
	}
	p := n.Page
	fail := invalid("invalid_response", "native wake history page has inconsistent selection, source or continuation metadata")
	if c.AppID != "" && c.AppID != appID || p.Mode != "keyset_v1" || p.Since != args.Since || p.Until != args.Until || p.Limit != args.Limit || p.Cursor != c.Native || p.Complete != (p.NextCursor == "") || p.SourceStatus != "read_succeeded" || p.CountScope != "returned_page" || len(n.Rows) > args.Limit || !p.Complete && len(n.Rows) != args.Limit {
		return fail
	}
	if c.LastAt != "" && len(n.Rows) > 0 {
		last, _ := wakeHistoryTime(c.LastAt)
		first, _ := wakeHistoryTime(n.Rows[0].At)
		if first.After(last) {
			return fail
		}
	}
	v.PaginationStatus = "keyset_started_at_instance_id_v1"
	v.PageComplete = &p.Complete
	v.NativeCandidateLimit = 0
	v.CandidateTruncation = "not_applicable_keyset"
	v.WindowVerification = "native_window_echo"
	v.WindowBounds = "inclusive_known_started_at"
	v.SourceStatus = p.SourceStatus
	v.CountBasis = "returned_page_instance_records"
	v.CapacityDenominator = "returned_page_instances_with_meta"
	v.Order = "native_started_at_instance_id_descending"
	for i := range v.Wakes {
		v.Wakes[i].StartedAtStatus = "observed_to_microsecond_precision"
		v.Wakes[i].WindowMembership = "verified_native_window"
	}
	if p.Complete {
		return nil
	}
	if p.NextCursor == c.Native || !m.safeWakeCursor(p.NextCursor, api.WakeHistoryCursorMaxBytes) {
		return fail
	}
	c.AppID, c.Native, c.LastAt = appID, p.NextCursor, n.Rows[len(n.Rows)-1].At
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	raw := "hp1." + base64.RawURLEncoding.EncodeToString(b)
	if !m.safeWakeCursor(raw, api.MCPWakeHistoryCursorMaxBytes) {
		return fail
	}
	next := args
	next.Cursor = raw
	v.NextCursor, v.NextArguments = raw, &next
	return nil
}
