package mcpmanagement

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type errorRequestView struct {
	LookupIdentifier string `json:"lookup_identifier"`
	ReceivedAt       string `json:"received_at"`
	Route            string `json:"route"`
	RouteModified    bool   `json:"route_modified"`
	HTTPStatus       int32  `json:"http_status"`
	ErrorClass       string `json:"error_class"`
}

type errorRequestsView struct {
	App                  string             `json:"app"`
	AppID                string             `json:"app_id"`
	Scope                string             `json:"scope"`
	Status               string             `json:"status"`
	Fingerprint          string             `json:"fingerprint"`
	HeaderStatus         string             `json:"header_status"`
	ErrorClass           string             `json:"error_class,omitempty"`
	Route                string             `json:"route,omitempty"`
	RouteModified        bool               `json:"route_modified"`
	HTTPStatus           int32              `json:"http_status,omitempty"`
	Requests             []errorRequestView `json:"requests"`
	ReturnedCount        int                `json:"returned_request_count"`
	Limit                int                `json:"limit"`
	PageComplete         bool               `json:"page_complete"`
	NextCursor           string             `json:"next_cursor,omitempty"`
	NextArguments        *errorRequestsArgs `json:"next_arguments,omitempty"`
	Filters              errorRequestsArgs  `json:"filters"`
	TextTruncated        bool               `json:"text_truncated"`
	SelectionBasis       string             `json:"selection_basis"`
	LookupBasis          string             `json:"lookup_basis"`
	EvidenceAvailability string             `json:"evidence_availability"`
	SourceCompleteness   string             `json:"source_completeness"`
	Freshness            string             `json:"freshness"`
	Order                string             `json:"order"`
	Limitations          string             `json:"limitations"`
}

func (m *management) projectErrorRequests(args errorRequestsArgs, appID string, limit int, cursor errorPageCursor, n api.AppErrorRequestsResponse) (errorRequestsView, error) {
	v := errorRequestsView{App: args.App, AppID: appID, Scope: "app_wide", Status: "observed", Fingerprint: args.Fingerprint, HeaderStatus: "not_reported", Requests: []errorRequestView{}, Limit: limit, PageComplete: n.NextCursor == "",
		Filters: errorRequestsArgs{App: args.App, Fingerprint: args.Fingerprint, Limit: limit}, SelectionBasis: "retained_fingerprint_history_without_summary_window_filter", LookupBasis: "public_request_id", EvidenceAvailability: "not_checked", SourceCompleteness: "unknown", Freshness: "unknown", Order: "received_at_desc_request_id_desc", Limitations: errorTriageLimitations}
	if n.Fingerprint != args.Fingerprint || len(n.Requests) > limit || n.NextCursor != "" && len(n.Requests) != limit || len(n.Requests) == 0 && cursor.Native == "" {
		return v, invalid("invalid_response", "native error request page changed fingerprint or has inconsistent pagination")
	}
	text := func(s string) string { return m.requestText(s, &v.TextTruncated) }
	if n.ErrorClass != "" {
		if !validErrorProperties(n.ErrorClass, n.HTTPStatus) {
			return v, invalid("invalid_response", "native error request header has invalid classification")
		}
		v.HeaderStatus, v.ErrorClass, v.HTTPStatus = "observed", n.ErrorClass, n.HTTPStatus
		v.Route = requestRoute(n.Route, text)
		v.RouteModified = v.Route != n.Route
	} else if len(n.Requests) > 0 || n.HTTPStatus != 0 || n.Route != "" {
		return v, invalid("invalid_response", "native error request header is missing")
	}
	for i, row := range n.Requests {
		if m.clean(row.RequestID) != row.RequestID {
			return v, invalid("invalid_response", "native error request identifier cannot be returned unchanged")
		}
		if err := validateErrorRequestRow(row, n, i); err != nil {
			return v, err
		}
		route := requestRoute(row.Route, text)
		v.Requests = append(v.Requests, errorRequestView{row.RequestID, row.ReceivedAt, route, route != row.Route, row.HTTPStatus, row.ErrorClass})
	}
	v.ReturnedCount = len(v.Requests)
	if len(v.Requests) == 0 {
		v.Status = "no_returned_error_requests"
	}
	cursor.AppID = appID
	next, err := m.writeErrorCursor(cursor, n.NextCursor)
	if err != nil {
		return v, err
	}
	v.NextCursor = next
	if next != "" {
		args := v.Filters
		args.Cursor = next
		v.NextArguments = &args
	}
	return v, nil
}

func validateErrorRequestRow(row api.AppErrorRequestItem, n api.AppErrorRequestsResponse, index int) error {
	id, err := uuid.Parse(row.RequestID)
	received, te := errorTime(row.ReceivedAt)
	if err != nil || id == uuid.Nil || id.String() != row.RequestID || !te || row.Route != n.Route || row.ErrorClass != n.ErrorClass || row.HTTPStatus != n.HTTPStatus || !validErrorProperties(row.ErrorClass, row.HTTPStatus) {
		return invalid("invalid_response", "native error request has an unsafe lookup identifier or inconsistent metadata")
	}
	if index > 0 {
		previous := n.Requests[index-1]
		at, _ := errorTime(previous.ReceivedAt)
		if received.After(at) || received.Equal(at) && row.RequestID >= previous.RequestID {
			return invalid("invalid_response", "native error request cursor tuples are duplicated or out of order")
		}
	}
	return nil
}
