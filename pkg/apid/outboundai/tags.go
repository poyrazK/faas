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

const (
	tagUsageInvalidDetail  = "date must be a YYYY-MM-DD UTC day within the last 31 days, key a tag key, and limit 1 to 1000"
	tagLimitsInvalidDetail = "limits must be at most 20 distinct key=value tags (keys of 1 to 32 lowercase letters, digits, _ . or - " +
		"starting with a letter; values of 1 to 64 letters, digits or . _ : @ + / -), each with a daily_cost_limit_millicents of " +
		"1 to 1000000000000, on an integration with an AI provider"
)

func (s *Service) tagStore(w http.ResponseWriter) (state.OutboundAITagStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAITagStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage tags are unavailable")
	}
	return store, ok
}

// GetTagUsage lists one UTC day's usage tags by cost, with their limits
// (ADR-1436).
func (s *Service) GetTagUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, api.OutboundAITagUsagePageMax)
	if !ok {
		problem(w, http.StatusBadRequest, api.CodeValidation, tagUsageInvalidDetail)
		return
	}
	store, ok := s.tagStore(w)
	if !ok {
		return
	}
	query := r.URL.Query()
	usage, err := store.GetOutboundAITagUsage(r.Context(), acct.ID, integrationID, query.Get("date"), query.Get("key"), limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, tagUsageInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, tagUsageResponse(usage))
	}
}

func tagUsageResponse(usage state.OutboundAITagUsage) api.OutboundAITagUsageResponse {
	out := api.OutboundAITagUsageResponse{UsageDate: usage.UsageDate, TrackedTags: usage.TrackedTags,
		Items: make([]api.OutboundAITagUsageItem, 0, len(usage.Items))}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAITagUsageItem{Tag: item.Tag, Requests: item.Requests,
			ErrorRequests: item.ErrorRequests, InputTokens: item.InputTokens, OutputTokens: item.OutputTokens,
			Tokens: item.InputTokens + item.OutputTokens, CostMillicents: item.CostMillicents,
			DailyCostLimitMillicents: copyInt64(item.DailyCostLimit)})
	}
	return out
}

// GetTagLimits returns an integration's tag limits (ADR-1436).
func (s *Service) GetTagLimits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.tagStore(w)
	if !ok {
		return
	}
	limits, err := store.ListOutboundAITagLimits(r.Context(), acct.ID, integrationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI tag limits are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.OutboundAITagLimits{Limits: limits})
	}
}

// PutTagLimits replaces an integration's tag limits (ADR-1436).
func (s *Service) PutTagLimits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAITagLimitsRequest
	if err := httpkit.DecodeJSONSized(r, &req, 8<<10); err != nil || !api.ValidOutboundAITagLimits(req.Limits) {
		problem(w, http.StatusBadRequest, api.CodeValidation, tagLimitsInvalidDetail)
		return
	}
	store, ok := s.tagStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAITagLimits(r.Context(), acct.ID, integrationID, req.Limits)
	s.writeResult(w, err, "outbound integration not found", tagLimitsInvalidDetail)
}

const tagExportInvalidDetail = "from and to must be YYYY-MM-DD UTC days within the last 31 days with from not after to, " +
	"key a tag key, and format csv or ndjson"

// ExportTagUsage streams the tag rows of the UTC days ?from= through ?to=
// (both default to today), optionally of one ?key=, as NDJSON (the
// default) or CSV, ordered by day and tag (ADR-1438).
func (s *Service) ExportTagUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	format := query.Get("format")
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "csv" {
		problem(w, http.StatusBadRequest, api.CodeValidation, tagExportInvalidDetail)
		return
	}
	store, ok := s.tagStore(w)
	if !ok {
		return
	}
	export := &tagUsageExport{w: w, format: format, label: integrationID}
	err := store.ExportOutboundAITagUsage(r.Context(), acct.ID, integrationID, query.Get("from"), query.Get("to"), query.Get("key"), export.write)
	switch {
	case export.started:
		// A failure after the first row can only cut the stream short.
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
		return
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, tagExportInvalidDetail)
		return
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
		return
	default:
		export.start()
	}
	if export.csv != nil {
		export.csv.Flush()
	}
}

// tagUsageExport writes tag export rows, sending the headers with the
// first row so an error before it can still be a problem response.
type tagUsageExport struct {
	w             http.ResponseWriter
	format, label string
	csv           *csv.Writer
	json          *json.Encoder
	started       bool
}

func (e *tagUsageExport) start() {
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
	e.w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "ai-tags-"+e.label+"."+e.format))
	e.w.WriteHeader(http.StatusOK)
	if e.format == "csv" {
		e.csv = csv.NewWriter(e.w)
		_ = e.csv.Write(api.OutboundAITagUsageExportColumns)
		return
	}
	e.json = json.NewEncoder(e.w)
}

func (e *tagUsageExport) write(row state.OutboundAITagUsageRow) error {
	e.start()
	tokens := row.InputTokens + row.OutputTokens
	if e.csv != nil {
		return e.csv.Write([]string{row.UsageDate, row.Tag, strconv.FormatInt(row.Requests, 10), strconv.FormatInt(row.ErrorRequests, 10),
			strconv.FormatInt(row.InputTokens, 10), strconv.FormatInt(row.OutputTokens, 10), strconv.FormatInt(tokens, 10),
			strconv.FormatInt(row.CostMillicents, 10)})
	}
	return e.json.Encode(api.OutboundAITagUsageExportRow{UsageDate: row.UsageDate, Tag: row.Tag, Requests: row.Requests,
		ErrorRequests: row.ErrorRequests, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, Tokens: tokens,
		CostMillicents: row.CostMillicents})
}
