package mcpmanagement

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

type wakePageCursor struct {
	Version int    `json:"v"`
	App     string `json:"app"`
	AppID   string `json:"app_id"`
	WakeID  string `json:"wake_id"`
	Since   string `json:"since"`
	Limit   int    `json:"limit"`
	Native  string `json:"native"`
	LastAt  string `json:"last_at"`
}

func (m *management) safeWakeCursor(s string, max int) bool {
	return s != "" && requestFilterText(s, max) && !strings.ContainsFunc(s, unicode.IsSpace) && m.clean(s) == s
}

func (m *management) readWakeCursor(args wakeTimelineArgs) (wakePageCursor, error) {
	c := wakePageCursor{Version: 1, App: args.App, WakeID: args.WakeID, Since: args.Since, Limit: args.Limit}
	if args.Cursor == "" {
		return c, nil
	}
	fail := invalid("invalid_argument", "wake cursor must preserve app, wake, normalized since and limit")
	s, ok := strings.CutPrefix(args.Cursor, "wp1.")
	if !ok || !m.safeWakeCursor(args.Cursor, api.MCPWakeTimelineCursorMaxBytes) {
		return c, fail
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return c, fail
	}
	var got wakePageCursor
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&got) != nil || d.Decode(new(any)) != io.EOF || got.Version != c.Version || got.App != c.App || got.WakeID != c.WakeID || got.Since != c.Since || got.Limit != c.Limit || validateSegment(got.AppID, "app_id") != nil || len(got.AppID) > api.MCPRequestIdentifierMaxBytes || m.clean(got.AppID) != got.AppID || !m.safeWakeCursor(got.Native, api.WakeTimelineCursorMaxBytes) {
		return c, fail
	}
	last, ok := wakeTimelineTime(got.LastAt)
	if !ok || last.IsZero() {
		return c, fail
	}
	return got, nil
}

func (m *management) projectWakePagination(args wakeTimelineArgs, appID string, n api.WakeTimelineResponse, v *wakeTimelineView) error {
	if n.Page == nil {
		if args.Cursor != "" {
			return invalid("native_upgrade_required", "native wake API no longer supplies keyset pagination")
		}
		return nil // Older API: one bounded legacy read, no manufactured cursor.
	}
	c, err := m.readWakeCursor(args)
	if err != nil {
		return err
	}
	p := n.Page
	fail := invalid("invalid_response", "native wake page has inconsistent mode, selection or continuation")
	if c.AppID != "" && c.AppID != appID {
		return fail
	}
	if p.Mode != "keyset_v1" || p.Since != args.Since || p.Cursor != c.Native || p.Complete != (p.NextCursor == "") || n.NextCursor != "" || !p.Complete && len(n.Events) == 0 {
		return fail
	}
	for _, row := range n.Events {
		at, _ := wakeTimelineTime(row.At)
		if !at.Equal(at.Truncate(time.Microsecond)) {
			return fail
		}
	}
	if c.LastAt != "" && len(n.Events) > 0 {
		last, _ := wakeTimelineTime(c.LastAt)
		first, _ := wakeTimelineTime(n.Events[0].At)
		if first.Before(last) {
			return fail
		}
	}
	v.PaginationStatus, v.PageComplete, v.NativeMoreEventsHint, v.NativeCursorAt = "keyset_at_id_v1", &p.Complete, !p.Complete, ""
	v.WindowVerification = "native_since_echo"
	if p.Complete {
		return nil
	}
	if p.NextCursor == c.Native || !m.safeWakeCursor(p.NextCursor, api.WakeTimelineCursorMaxBytes) {
		return fail
	}
	c.AppID, c.Native, c.LastAt = appID, p.NextCursor, n.Events[len(n.Events)-1].At
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	raw := "wp1." + base64.RawURLEncoding.EncodeToString(data)
	if !m.safeWakeCursor(raw, api.MCPWakeTimelineCursorMaxBytes) {
		return fail
	}
	next := args
	next.Cursor = raw
	v.NextCursor, v.NextArguments = raw, &next
	return nil
}
