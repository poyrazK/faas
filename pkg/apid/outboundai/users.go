package outboundai

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const userUsageInvalidDetail = "date must be a YYYY-MM-DD UTC day within the last 31 days, end_user 1 to 64 letters, digits or . _ : @ + = / | -, and limit 1 to 1,000"

// GetUserUsage lists one UTC day of AI usage by end user, most expensive
// first (ADR-994). ?end_user= reads one user; ?date= and ?limit= page.
func (s *Service) GetUserUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, api.OutboundAIUserUsagePageMax)
	if !ok {
		problem(w, http.StatusBadRequest, api.CodeValidation, userUsageInvalidDetail)
		return
	}
	store, ok := s.Host.Store().(state.OutboundAIUserStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
		return
	}
	query := r.URL.Query()
	usage, err := store.GetOutboundAIUserUsage(r.Context(), acct.ID, integrationID, query.Get("date"), query.Get("end_user"), limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, userUsageInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, userUsageResponse(usage))
	}
}

const userExportInvalidDetail = "from and to must be YYYY-MM-DD UTC days within the last 31 days with from not after to, and format csv or ndjson"

// ExportUserUsage streams the per-end-user rows of the UTC days ?from=
// through ?to= (both default to today) as NDJSON (the default) or CSV
// (ADR-995). Rows are ordered by day and end user.
func (s *Service) ExportUserUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	format := query.Get("format")
	if format == "" {
		format = "ndjson"
	}
	store, storeOK := s.Host.Store().(state.OutboundAIUserStore)
	switch {
	case format != "ndjson" && format != "csv":
		problem(w, http.StatusBadRequest, api.CodeValidation, userExportInvalidDetail)
		return
	case !storeOK:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
		return
	}
	export := newUserUsageExport(w, format, integrationID)
	err := store.ExportOutboundAIUserUsage(r.Context(), acct.ID, integrationID, query.Get("from"), query.Get("to"), export.write)
	switch {
	case export.started:
		// A failure after the first row can only cut the stream short.
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
		return
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, userExportInvalidDetail)
		return
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
		return
	default:
		export.start()
	}
	export.finish()
}

// userUsageExport writes export rows, sending the headers with the first
// row so an error before it can still be a problem response.
type userUsageExport struct {
	w             http.ResponseWriter
	format, label string
	csv           *csv.Writer
	json          *json.Encoder
	started       bool
}

func newUserUsageExport(w http.ResponseWriter, format, integrationID string) *userUsageExport {
	return &userUsageExport{w: w, format: format, label: integrationID}
}

func (e *userUsageExport) start() {
	if e.started {
		return
	}
	e.started = true
	contentType := "application/x-ndjson; charset=utf-8"
	if e.format == "csv" {
		contentType = "text/csv; charset=utf-8"
	}
	e.w.Header().Set("Cache-Control", "no-store")
	e.w.Header().Set("Content-Type", contentType)
	e.w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "ai-users-"+e.label+"."+e.format))
	e.w.WriteHeader(http.StatusOK)
	if e.format == "csv" {
		e.csv = csv.NewWriter(e.w)
		_ = e.csv.Write(api.OutboundAIUserUsageExportColumns)
		return
	}
	e.json = json.NewEncoder(e.w)
}

func (e *userUsageExport) write(row state.OutboundAIUserUsageRow) error {
	e.start()
	if e.csv != nil {
		return e.csv.Write([]string{row.UsageDate, row.EndUser, strconv.FormatInt(row.Requests, 10),
			strconv.FormatInt(row.ErrorRequests, 10), strconv.FormatInt(row.InputTokens, 10), strconv.FormatInt(row.OutputTokens, 10),
			strconv.FormatInt(row.InputTokens+row.OutputTokens, 10), strconv.FormatInt(row.CostMillicents, 10)})
	}
	return e.json.Encode(api.OutboundAIUserUsageExportRow{UsageDate: row.UsageDate, OutboundAIUserUsageItem: api.OutboundAIUserUsageItem{
		EndUser: row.EndUser, Requests: row.Requests, ErrorRequests: row.ErrorRequests, InputTokens: row.InputTokens,
		OutputTokens: row.OutputTokens, Tokens: row.InputTokens + row.OutputTokens, CostMillicents: row.CostMillicents}})
}

func (e *userUsageExport) finish() {
	if e.csv != nil {
		e.csv.Flush()
	}
}

func userUsageResponse(usage state.OutboundAIUserUsage) api.OutboundAIUserUsageResponse {
	out := api.OutboundAIUserUsageResponse{UsageDate: usage.UsageDate, TrackedUsers: usage.TrackedUsers,
		UserDailyTokenLimit: copyInt64(usage.UserDailyTokenLimit), UserDailyCostLimitMillicents: copyInt64(usage.UserDailyCostLimit),
		Items: make([]api.OutboundAIUserUsageItem, 0, len(usage.Items))}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAIUserUsageItem{EndUser: item.EndUser, Requests: item.Requests,
			ErrorRequests: item.ErrorRequests, InputTokens: item.InputTokens, OutputTokens: item.OutputTokens,
			Tokens: item.InputTokens + item.OutputTokens, CostMillicents: item.CostMillicents})
	}
	return out
}
