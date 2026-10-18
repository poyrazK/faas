package datarepair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// SourceQuery uses an inclusive Since and exclusive Before. Listing is live
// metadata discovery, not a frozen repair selection or an eligibility check.
type SourceQuery struct {
	Release string    `json:"release,omitempty"`
	Table   string    `json:"table,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Before  time.Time `json:"before,omitzero"`
	Limit   int       `json:"limit"`
	Cursor  string    `json:"cursor,omitempty"`
}

type SourceRepair struct {
	PlanID      string    `json:"plan_id"`
	OperationID string    `json:"operation_id"`
	AppliedAt   time.Time `json:"applied_at"`
}

type Source struct {
	ID              string        `json:"id"`
	Release         string        `json:"release"`
	Table           string        `json:"table"`
	CreatedAt       time.Time     `json:"created_at"`
	RepairOf        *string       `json:"repair_of,omitempty"`
	ChangeRows      *int          `json:"change_rows"`
	PayloadPurgedAt *time.Time    `json:"payload_purged_at,omitempty"`
	AppliedRepairs  int           `json:"applied_repairs"`
	LatestRepair    *SourceRepair `json:"latest_repair,omitempty"`
	CanPrepare      bool          `json:"can_prepare"`
	SourceState     string        `json:"source_state,omitempty"`
}

type SourcePage struct {
	Scope      Scope    `json:"scope"`
	Items      []Source `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func (q SourceQuery) Normalize() (SourceQuery, error) {
	if q.Limit == 0 {
		q.Limit = api.DataRepairSourcePageSize
	}
	for _, s := range []string{q.Release, q.Table} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) || len(s) > api.DataRepairSourceMaxFilterBytes {
			return q, ErrInvalid
		}
	}
	if q.Limit < 1 || q.Limit > api.DataRepairSourceMaxPageSize || len(q.Cursor) > api.DataRepairSourceMaxCursorBytes ||
		(!q.Since.IsZero() && !q.Before.IsZero() && !q.Since.Before(q.Before)) ||
		q.Since.Year() < 1 || q.Since.Year() > 9999 || q.Before.Year() < 1 || q.Before.Year() > 9999 {
		return q, ErrInvalid
	}
	q.Since, q.Before = q.Since.UTC(), q.Before.UTC()
	return q, nil
}

type sourceCursor struct {
	Version     int       `json:"v"`
	Fingerprint string    `json:"f"`
	CreatedAt   time.Time `json:"t"`
	ID          string    `json:"id"`
}

func sourceFingerprint(s Scope, q SourceQuery) string {
	q.Cursor = ""
	b, _ := json.Marshal(struct {
		Scope Scope
		Query SourceQuery
	}{s, q})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (e *Engine) Sources(ctx context.Context, s Scope, q SourceQuery) (SourcePage, error) {
	if err := e.ready(s); err != nil {
		return SourcePage{}, err
	}
	q, err := q.Normalize()
	if err != nil {
		return SourcePage{}, err
	}
	fingerprint := sourceFingerprint(s, q)
	var afterTime, afterID any
	if q.Cursor != "" {
		var cursor sourceCursor
		if DecodeSourceCursor(q.Cursor, &cursor) != nil || cursor.Version != 1 || cursor.Fingerprint != fingerprint || !validID(cursor.ID) || cursor.CreatedAt.IsZero() {
			return SourcePage{}, ErrInvalid
		}
		afterTime, afterID = cursor.CreatedAt, cursor.ID
	}
	var raw []byte
	if err := e.Pool.QueryRow(ctx, `SELECT gregale_repair.list_sources($1::jsonb,$2,$3,$4::timestamptz,$5::timestamptz,$6::timestamptz,$7::uuid,$8::integer)`,
		scopeJSON(s), optionalString(q.Release), optionalString(q.Table), optionalTime(q.Since), optionalTime(q.Before), afterTime, afterID, q.Limit).Scan(&raw); err != nil {
		return SourcePage{}, classify(err)
	}
	p := SourcePage{Scope: s}
	if len(raw) > api.DataRepairSourceMaxResponseBytes || json.Unmarshal(raw, &p.Items) != nil || p.Items == nil || len(p.Items) > q.Limit+1 {
		return SourcePage{}, ErrUnavailable
	}
	if len(p.Items) > q.Limit {
		p.Items = p.Items[:q.Limit]
		last := p.Items[len(p.Items)-1]
		p.NextCursor = EncodeSourceCursor(sourceCursor{1, fingerprint, last.CreatedAt, last.ID})
	}
	for i := range p.Items {
		p.Items[i].CanPrepare = p.Items[i].PayloadPurgedAt == nil
	}
	return p, nil
}

// Cursors bind continuation to a scope/filter or configured binding. They are
// opaque navigation hints, not credentials; all reads authenticate independently.
func EncodeSourceCursor(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeSourceCursor(raw string, v any) error {
	if raw == "" || len(raw) > api.DataRepairSourceMaxCursorBytes {
		return ErrInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(b) != raw {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func optionalString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
