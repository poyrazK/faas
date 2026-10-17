// Package liveproto is the live-query frame contract (ADR-1384): the JSON
// frames a client and realtimed exchange over an app's realtime endpoint, and
// Result, the reference client that applies them. The JavaScript SDK runs the
// same session fixtures (testdata/sessions.json) as this package's tests.
//
// A subscription's result has a version. live.rows replaces the result and
// sets the version; live.changes moves it from Base to Version. Each frame
// carries the snapshot the result now reflects, so a client knows exactly
// which transactions it has seen: awaitTxId resolves once the snapshot makes
// the transaction visible. After every round realtimed sends live.progress
// for each database: every live (not stale) subscription on that database
// now reflects that snapshot, even when its rows did not change.
//
// Sealed queries expire. live.rows and live.renewed say when; before then the
// client sends live.renew with a fresh seal for the same query. A subscription
// whose seal lapses turns stale (live.error seal_expired, not closed) and
// receives nothing until it is renewed.
package liveproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// Frame types.
const (
	TypeSubscribe   = "live.subscribe"
	TypeRenew       = "live.renew"
	TypeUnsubscribe = "live.unsubscribe"

	TypeRows     = "live.rows"
	TypeChanges  = "live.changes"
	TypeProgress = "live.progress"
	TypeRenewed  = "live.renewed"
	TypeError    = "live.error"
)

// Change operations.
const (
	OpUpsert = "upsert"
	OpRemove = "remove"
)

// Error codes. Closed reports whether a code ends the subscription.
const (
	CodeSealExpired   = "seal_expired"   // stale: renew with a fresh seal
	CodeSealInvalid   = "seal_invalid"   // the seal broke a limit or its lifetime rules
	CodeSealRejected  = "seal_rejected"  // not authentic, unknown or retired key
	CodeForbidden     = "forbidden"      // the endpoint may not serve that database
	CodeRenewMismatch = "renew_mismatch" // a renewal sealed a different query
	CodeUnsupported   = "unsupported_query"
	CodeInvalidQuery  = "invalid_query" // PostgreSQL refused the query
	CodeSlowConsumer  = "slow_consumer" // the connection fell too far behind
	CodeLimit         = "live_query_limit"
	CodeSchemaChanged = "schema_changed" // resubscribe with a fresh seal
	CodeUnavailable   = "unavailable"    // stale until the database is back
	CodeInternal      = "internal"
	CodeProtocol      = "protocol_error"
)

// ErrProtocol marks a frame that breaks the contract.
var ErrProtocol = errors.New("liveproto: protocol violation")

// ClientFrame is a frame a client sends.
type ClientFrame struct {
	Type         string `json:"type"`
	Subscription string `json:"subscription"` // chosen by the client, unique per connection
	Sealed       string `json:"sealed,omitempty"`
	// Version is the result version the client already holds when it
	// resubscribes after a reconnect. realtimed sends the changes since then
	// when it still holds them, and a full live.rows otherwise.
	Version uint64 `json:"version,omitempty"`
}

// ParseClientFrame decodes and validates a client frame.
func ParseClientFrame(data []byte) (ClientFrame, error) {
	if len(data) > api.LiveQueryClientFrameMaxBytes {
		return ClientFrame{}, fmt.Errorf("%w: frame is %d bytes, limit %d", ErrProtocol, len(data), api.LiveQueryClientFrameMaxBytes)
	}
	var f ClientFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return ClientFrame{}, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if !validSubscription(f.Subscription) {
		return ClientFrame{}, fmt.Errorf("%w: subscription ID must be 1-%d characters of A-Z a-z 0-9 _ -", ErrProtocol, api.LiveQuerySubscriptionIDMax)
	}
	switch f.Type {
	case TypeSubscribe:
	case TypeRenew:
		if f.Version != 0 {
			return ClientFrame{}, fmt.Errorf("%w: %s carries no version", ErrProtocol, f.Type)
		}
	case TypeUnsubscribe:
		if f.Sealed != "" || f.Version != 0 {
			return ClientFrame{}, fmt.Errorf("%w: %s carries only the subscription", ErrProtocol, f.Type)
		}
		return f, nil
	default:
		return ClientFrame{}, fmt.Errorf("%w: unknown frame type %q", ErrProtocol, f.Type)
	}
	if f.Sealed == "" {
		return ClientFrame{}, fmt.Errorf("%w: %s needs a sealed query", ErrProtocol, f.Type)
	}
	return f, nil
}

func validSubscription(s string) bool {
	if s == "" || len(s) > api.LiveQuerySubscriptionIDMax {
		return false
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// Column is a result column: its name and PostgreSQL type name (int8, text,
// timestamptz, jsonb, _int4, ...). Values travel in PostgreSQL text form.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Row is a result row: an opaque ID, stable while the row's identity holds,
// and its values in PostgreSQL text form (null is SQL NULL).
type Row struct {
	ID     string    `json:"id"`
	Values []*string `json:"values"`
}

// Rows replaces a subscription's result. A large result spans several frames
// with the same version; the client applies them together once Complete.
type Rows struct {
	Type         string   `json:"type"`
	Subscription string   `json:"subscription"`
	Database     string   `json:"database"`
	Version      uint64   `json:"version"`
	Snapshot     string   `json:"snapshot"`
	Expires      int64    `json:"expires"` // the seal's expiry, Unix seconds
	Columns      []Column `json:"columns"`
	Ordered      bool     `json:"ordered,omitempty"` // rows arrive in the query's order
	Rows         []Row    `json:"rows"`
	Complete     bool     `json:"complete"`
}

// Change is one row change.
type Change struct {
	Op     string    `json:"op"`
	ID     string    `json:"id"`
	Values []*string `json:"values,omitempty"`
}

// Changes moves a result from version Base to Version.
type Changes struct {
	Type         string   `json:"type"`
	Subscription string   `json:"subscription"`
	Base         uint64   `json:"base"`
	Version      uint64   `json:"version"`
	Snapshot     string   `json:"snapshot"`
	Xids         []string `json:"xids"` // the transactions applied, as decimal xid8
	Changes      []Change `json:"changes"`
	// Order is an ordered result's row IDs, in order, after the changes. It
	// is present whenever the changes add rows or move one; otherwise the
	// remaining rows keep their order.
	Order []string `json:"order,omitempty"`
}

// Progress says every live subscription on Database reflects Snapshot.
type Progress struct {
	Type     string `json:"type"`
	Database string `json:"database"`
	Snapshot string `json:"snapshot"`
}

// Renewed acknowledges live.renew.
type Renewed struct {
	Type         string `json:"type"`
	Subscription string `json:"subscription"`
	Expires      int64  `json:"expires"`
}

// Error reports a refused frame or a subscription that stopped. A closed
// subscription is gone; otherwise it is stale until renewed or resumed.
type Error struct {
	Type         string `json:"type"`
	Subscription string `json:"subscription,omitempty"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	Closed       bool   `json:"closed"`
	// Limit errors (RFC 7807 style) name the limit, the observed value and
	// the documentation.
	Limit    int64  `json:"limit,omitempty"`
	Observed int64  `json:"observed,omitempty"`
	Docs     string `json:"docs,omitempty"`
}

// ParseServerFrame decodes a server frame into its type.
func ParseServerFrame(data []byte) (any, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	var out any
	switch head.Type {
	case TypeRows:
		out = &Rows{}
	case TypeChanges:
		out = &Changes{}
	case TypeProgress:
		out = &Progress{}
	case TypeRenewed:
		out = &Renewed{}
	case TypeError:
		out = &Error{}
	default:
		return nil, fmt.Errorf("%w: unknown frame type %q", ErrProtocol, head.Type)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrProtocol, head.Type, err)
	}
	return out, nil
}

// LimitError is a result that breaks a live_query_limit.
type LimitError struct {
	What            string
	Limit, Observed int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("liveproto: %s is %d bytes, limit %d", e.What, e.Observed, e.Limit)
}

// Split cuts a result into frames of at most maxBytes each, all with f's
// version; the last one is Complete. A single row larger than a frame is an
// error: the result breaks the live_query_limit on bytes per row.
func (f Rows) Split(maxBytes int) ([]Rows, error) { return f.SplitWithin(maxBytes, 0) }

// SplitWithin is Split for a result of at most maxTotal bytes across its
// frames (zero: unbounded); a larger result is a *LimitError.
func (f Rows) SplitWithin(maxBytes, maxTotal int) ([]Rows, error) {
	f.Type = TypeRows
	f.Complete = false
	rows := f.Rows
	f.Rows = nil
	empty, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("liveproto: encode rows: %w", err)
	}
	var out []Rows
	cur := f
	size := len(empty) + len(`,"complete":true`)
	total := 0 // bytes of rows, across frames
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("liveproto: encode row: %w", err)
		}
		if len(empty)+len(b)+len(`,"complete":true`) > maxBytes {
			return nil, &LimitError{What: "row " + r.ID, Limit: int64(maxBytes), Observed: int64(len(b))}
		}
		if total += len(b) + 1; maxTotal > 0 && total > maxTotal {
			return nil, &LimitError{What: "the result", Limit: int64(maxTotal), Observed: int64(total)}
		}
		if size+len(b)+1 > maxBytes && len(cur.Rows) > 0 {
			out = append(out, cur)
			cur = f
			size = len(empty) + len(`,"complete":true`)
		}
		cur.Rows = append(cur.Rows, r)
		size += len(b) + 1
	}
	if cur.Rows == nil {
		cur.Rows = []Row{}
	}
	cur.Complete = true
	return append(out, cur), nil
}

// FormatXids renders xid8s for Changes.Xids.
func FormatXids(xids []uint64) []string {
	out := make([]string, len(xids))
	for i, x := range xids {
		out[i] = strconv.FormatUint(x, 10)
	}
	return out
}
