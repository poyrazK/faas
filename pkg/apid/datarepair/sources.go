package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

// SourceReader is additive: an older executor can still reconcile its plans
// without claiming support for the metadata discovery contract.
type SourceReader interface {
	Sources(context.Context, core.Scope, core.SourceQuery) (core.SourcePage, error)
}

func sourceQuery(raw string, hosted bool) (core.SourceQuery, url.Values, error) {
	q := core.SourceQuery{}
	if len(raw) > api.DataRepairMaxRequestBytes {
		return q, nil, core.ErrInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, nil, core.ErrInvalid
	}
	for key, v := range values {
		if len(v) != 1 {
			return q, nil, core.ErrInvalid
		}
		switch key {
		case "release", "table", "since", "before", "limit", "cursor":
		case "environment", "customer":
			if hosted {
				continue
			}
			fallthrough
		default:
			return q, nil, core.ErrInvalid
		}
	}
	q.Release, q.Table, q.Cursor = values.Get("release"), values.Get("table"), values.Get("cursor")
	if rawLimit, ok := values["limit"]; ok {
		q.Limit, err = strconv.Atoi(rawLimit[0])
		if err != nil || q.Limit < 1 {
			return q, nil, core.ErrInvalid
		}
	}
	for key, target := range map[string]*time.Time{"since": &q.Since, "before": &q.Before} {
		if rawTime, ok := values[key]; ok {
			*target, err = time.Parse(time.RFC3339Nano, rawTime[0])
			if err != nil || target.IsZero() {
				return q, nil, core.ErrInvalid
			}
		}
	}
	q, err = q.Normalize()
	return q, values, err
}

func SourceQueryValues(q core.SourceQuery) url.Values {
	v := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	for key, value := range map[string]string{"release": q.Release, "table": q.Table, "cursor": q.Cursor} {
		if value != "" {
			v.Set(key, value)
		}
	}
	for key, value := range map[string]time.Time{"since": q.Since, "before": q.Before} {
		if !value.IsZero() {
			v.Set(key, value.Format(time.RFC3339Nano))
		}
	}
	return v
}

func (h *Handler) sources(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	q, _, err := sourceQuery(r.URL.RawQuery, false)
	if err != nil {
		problem(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	page, err := h.Engine.Sources(ctx, p.Scope, q)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}

func (c *Remote) Sources(ctx context.Context, s core.Scope, q core.SourceQuery) (core.SourcePage, error) {
	q, err := q.Normalize()
	if err != nil || s.Validate() != nil {
		return core.SourcePage{}, core.ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/v1/data-repair/sources?"+SourceQueryValues(q).Encode(), nil)
	if err != nil {
		return core.SourcePage{}, core.ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(r)
	if err != nil {
		return core.SourcePage{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			return core.SourcePage{}, core.ErrInvalid
		}
		return core.SourcePage{}, core.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairSourceMaxResponseBytes+1)))
	var p core.SourcePage
	if err != nil || len(raw) > api.DataRepairSourceMaxResponseBytes || json.Unmarshal(raw, &p) != nil || p.Scope != s || p.Items == nil || len(p.Items) > q.Limit || len(p.NextCursor) > api.DataRepairSourceMaxCursorBytes {
		return core.SourcePage{}, core.ErrUnavailable
	}
	return p, nil
}

type SourceScopesResponse struct {
	Scopes []core.Scope `json:"scopes"`
}

func (s *Managed) SourceScopes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	response := SourceScopesResponse{Scopes: []core.Scope{}}
	for _, b := range s.Bindings {
		if b.Scope.AccountID == acct.ID && b.Scope.AppID == app.ID && b.Executor != nil {
			response.Scopes = append(response.Scopes, b.Scope)
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Managed) sourceBinding(account, app string, values url.Values) (Binding, error) {
	var found *Binding
	for _, b := range s.Bindings {
		if b.Scope.AccountID != account || b.Scope.AppID != app || b.Executor == nil ||
			(values.Has("environment") && b.Scope.Environment != values.Get("environment")) ||
			(values.Has("customer") && b.Scope.PlatformTenantID != values.Get("customer")) {
			continue
		}
		if found != nil {
			return Binding{}, core.ErrInvalid
		}
		found = &b
	}
	if found == nil {
		return Binding{}, core.ErrNotFound
	}
	return *found, nil
}

type boundSourceCursor struct {
	Binding string `json:"binding"`
	Cursor  string `json:"cursor"`
}

func (s *Managed) Sources(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	q, values, err := sourceQuery(r.URL.RawQuery, true)
	if err != nil {
		problem(w, err)
		return
	}
	b, err := s.sourceBinding(acct.ID, app.ID, values)
	if err != nil {
		if errors.Is(err, core.ErrInvalid) {
			api.WriteProblem(w, api.NewProblem(400, "repair_invalid", "Select a repair source scope", "Choose one configured environment and customer from source-scopes"))
		} else {
			problem(w, err)
		}
		return
	}
	page, err := s.sourcePage(r.Context(), b, q)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}

func (s *Managed) sourcePage(ctx context.Context, b Binding, q core.SourceQuery) (core.SourcePage, error) {
	reader, ok := b.Executor.(SourceReader)
	if !ok {
		return core.SourcePage{}, core.ErrUnavailable
	}
	if q.Cursor != "" {
		var cursor boundSourceCursor
		if core.DecodeSourceCursor(q.Cursor, &cursor) != nil || cursor.Binding != b.Identity || cursor.Cursor == "" {
			return core.SourcePage{}, core.ErrInvalid
		}
		q.Cursor = cursor.Cursor
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := reader.Sources(ctx, b.Scope, q)
	if err != nil {
		return core.SourcePage{}, err
	}
	if p.Scope != b.Scope || p.Items == nil || len(p.Items) > q.Limit {
		return core.SourcePage{}, core.ErrUnavailable
	}
	store, _, ok := s.store()
	if !ok {
		return core.SourcePage{}, core.ErrUnavailable
	}
	for i := range p.Items {
		item := &p.Items[i]
		op, err := store.OperationByID(ctx, b.Scope.AccountID, "", item.ID)
		item.CanPrepare = false
		item.SourceState = "not_retained"
		if err == nil && repairScope(op) == b.Scope && op.DataRepair != nil {
			item.SourceState = "repair_operation"
		} else if err == nil && repairScope(op) == b.Scope {
			item.SourceState = "unsettled"
			if op.State.Terminal() {
				item.SourceState = "settled"
				item.CanPrepare = item.PayloadPurgedAt == nil
			}
		} else if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrOperationExpired) {
			return core.SourcePage{}, core.ErrUnavailable
		}
	}
	if p.NextCursor != "" {
		p.NextCursor = core.EncodeSourceCursor(boundSourceCursor{b.Identity, p.NextCursor})
		if len(p.NextCursor) > api.DataRepairSourceMaxCursorBytes {
			return core.SourcePage{}, core.ErrUnavailable
		}
	}
	return p, nil
}
