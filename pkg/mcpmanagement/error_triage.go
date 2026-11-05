package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type errorGroupsArgs struct {
	App    string `json:"app" jsonschema:"App slug; requires diagnostic consent"`
	Since  string `json:"since,omitempty" jsonschema:"RFC3339 start of the last-seen window; default 24h before until"`
	Until  string `json:"until,omitempty" jsonschema:"RFC3339 end of the last-seen window; default now"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 50; default 20"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; copy next_arguments unchanged"`
}

type errorRequestsArgs struct {
	App         string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Fingerprint string `json:"fingerprint" jsonschema:"Exact unchanged 64-character lowercase hexadecimal error fingerprint"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Native page size from 1 to 50; default 20. Repeat unchanged with a continuation."`
	Cursor      string `json:"cursor,omitempty" jsonschema:"Opaque MCP next_cursor, at most 8192 bytes; copy next_arguments unchanged. Bound to app identity, fingerprint and page size."`
}

func validErrorFingerprint(s string) bool {
	_, err := hex.DecodeString(s)
	if len(s) != sha256.Size*2 || err != nil {
		return false
	}
	for _, c := range s {
		if c >= 'A' && c <= 'F' {
			return false
		}
	}
	return true
}

func errorTime(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	return t, err == nil && requestFilterText(raw, api.MCPErrorTimeMaxBytes)
}

func (m *management) errorGroupsSelection(args errorGroupsArgs) (api.AppErrorsSummaryOptions, errorPageCursor, error) {
	var opts api.AppErrorsSummaryOptions
	limit, err := errorPageLimit(args.App, args.Limit)
	if err != nil {
		return opts, errorPageCursor{}, err
	}
	if args.Cursor != "" && (args.Since == "" || args.Until == "") {
		return opts, errorPageCursor{}, invalid("invalid_argument", "continuations require the explicit since/until from next_arguments")
	}
	now := time.Now().UTC()
	until := now
	if args.Until != "" {
		var ok bool
		until, ok = errorTime(args.Until)
		if !ok || until.After(now) {
			return opts, errorPageCursor{}, invalid("invalid_argument", "until must be an RFC3339 timestamp at or before now")
		}
	}
	// Native PostgreSQL parameters discard sub-microsecond precision. Send
	// that exact selection and expose it in filters/continuations instead
	// of rejecting legitimate boundary rows against a nanosecond echo.
	until = until.UTC().Truncate(time.Microsecond)
	since := until.Add(-24 * time.Hour)
	if args.Since != "" {
		var ok bool
		since, ok = errorTime(args.Since)
		if !ok {
			return opts, errorPageCursor{}, invalid("invalid_argument", "since must be an RFC3339 timestamp")
		}
		since = since.UTC().Truncate(time.Microsecond)
	}
	if !since.Before(until) {
		return opts, errorPageCursor{}, invalid("invalid_argument", "since must be earlier than until after native microsecond normalization")
	}
	opts.Since, opts.Until, opts.Limit = since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano), limit
	c, err := m.readErrorCursor(args.Cursor, errorPageCursor{Version: 1, Kind: "groups", App: args.App, Since: opts.Since, Until: opts.Until, Limit: limit})
	opts.Cursor = c.Native
	return opts, c, err
}

func (m *management) listErrorGroups(ctx context.Context, args errorGroupsArgs) (any, error) {
	opts, cursor, err := m.errorGroupsSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if cursor.AppID != "" && cursor.AppID != app.ID {
		return nil, invalid("invalid_argument", "continuation app identity changed; start a new first page")
	}
	n, err := m.client.GetAppErrorsSummary(ctx, args.App, opts)
	if err != nil {
		return nil, err
	}
	v, err := m.projectErrorGroups(args.App, app.ID, opts, cursor, n)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, v), nil
}

func (m *management) listErrorRequests(ctx context.Context, args errorRequestsArgs) (any, error) {
	limit, err := errorPageLimit(args.App, args.Limit)
	if err != nil {
		return nil, err
	}
	if !validErrorFingerprint(args.Fingerprint) || m.clean(args.Fingerprint) != args.Fingerprint {
		return nil, invalid("invalid_argument", "fingerprint must be exactly 64 lowercase hexadecimal characters")
	}
	cursor, err := m.readErrorCursor(args.Cursor, errorPageCursor{Version: 1, Kind: "requests", App: args.App, Fingerprint: args.Fingerprint, Limit: limit})
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if cursor.AppID != "" && cursor.AppID != app.ID {
		return nil, invalid("invalid_argument", "continuation app identity changed; start a new first page")
	}
	n, err := m.client.ListAppErrorRequests(ctx, args.App, args.Fingerprint, cursor.Native, limit)
	if err != nil {
		return nil, err
	}
	v, err := m.projectErrorRequests(args, app.ID, limit, cursor, n)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, v), nil
}

func errorPageLimit(app string, limit int) (int, error) {
	if err := validateSegment(app, "app"); err != nil {
		return 0, err
	}
	if limit == 0 {
		limit = api.MCPErrorPageDefault
	}
	if limit < 1 || limit > api.MCPErrorPageMax {
		return 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return limit, nil
}
