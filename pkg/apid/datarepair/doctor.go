package datarepair

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

// DoctorReader is additive. Executors without catalog diagnostics remain
// usable for existing work but cannot claim readiness.
type DoctorReader interface {
	Doctor(context.Context, core.Scope, string) (core.DoctorReport, error)
}

func doctorQuery(raw string, hosted bool) (url.Values, error) {
	if len(raw) > api.DataRepairMaxRequestBytes {
		return nil, core.ErrInvalid
	}
	v, err := url.ParseQuery(raw)
	if err != nil || !core.DoctorTable(v.Get("table")) {
		return nil, core.ErrInvalid
	}
	for key, values := range v {
		allowed := key == "table" || hosted && (key == "environment" || key == "customer")
		if len(values) != 1 || !allowed {
			return nil, core.ErrInvalid
		}
	}
	return v, nil
}

func (h *Handler) doctor(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	values, err := doctorQuery(r.URL.RawQuery, false)
	if err != nil {
		problem(w, err)
		return
	}
	report, err := h.Engine.Doctor(r.Context(), p.Scope, values.Get("table"))
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (s *Managed) Doctor(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	values, err := doctorQuery(r.URL.RawQuery, true)
	if err != nil {
		problem(w, err)
		return
	}
	b, err := s.sourceBinding(acct.ID, app.ID, values)
	if err != nil {
		problem(w, err)
		return
	}
	report := boundDoctor(r.Context(), b, values.Get("table"))
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func boundDoctor(ctx context.Context, b Binding, table string) core.DoctorReport {
	report := core.UnavailableDoctor(b.Scope, table)
	if reader, ok := b.Executor.(DoctorReader); ok {
		ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
		defer cancel()
		got, err := reader.Doctor(ctx, b.Scope, table)
		if err == nil && got.Valid() && len(got.Findings) == 9 && got.Scope == b.Scope && got.Table == table {
			report = got
		}
	}
	report.BoundDoctor()
	return report
}

func (c *Remote) Doctor(ctx context.Context, s core.Scope, table string) (core.DoctorReport, error) {
	if s.Validate() != nil || !core.DoctorTable(table) {
		return core.DoctorReport{}, core.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/v1/data-repair/doctor?"+url.Values{"table": {table}}.Encode(), nil)
	if err != nil {
		return core.DoctorReport{}, core.ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(r)
	if err != nil {
		return core.DoctorReport{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return core.DoctorReport{}, core.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairDoctorMaxResponseBytes+1)))
	var report core.DoctorReport
	if err != nil || len(raw) > api.DataRepairDoctorMaxResponseBytes || json.Unmarshal(raw, &report) != nil || !report.Valid() || len(report.Findings) != 9 || report.Scope != s || report.Table != table {
		return core.DoctorReport{}, core.ErrUnavailable
	}
	return report, nil
}
