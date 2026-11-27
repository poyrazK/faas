package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

const obsFetchRuntimeBusyReason = "runtime_busy"

// This error owns only reviewed metadata and a safe CLI message. Never keep
// an underlying transport/decoder error: it may contain URLs or credentials.
type obsFetchFailure struct {
	reason, message string
	status          int
}

func (e *obsFetchFailure) Error() string { return e.message }

func obsFetchFailed(section, reason, message string, status int) error {
	return &obsFetchFailure{reason: reason, message: section + ": " + message, status: status}
}

func obsFetchTransportReason(ctx context.Context, err error, fallback string) string {
	if ctx.Err() != nil {
		return obsFetchErrorReason(ctx.Err(), fallback)
	}
	return obsFetchErrorReason(err, fallback)
}

func obsFetchErrorReason(err error, fallback string) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return fallback
}

func obsFetchResponseFailure(section string, resp *http.Response) error {
	if section == "runtime-builds" && resp.StatusCode == http.StatusTooManyRequests {
		codes := resp.Header.Values(api.ErrorCodeHeader)
		if len(codes) == 1 && codes[0] == api.CodeObsRuntimeBusy {
			return obsFetchFailed(section, obsFetchRuntimeBusyReason, "apid runtime collector is busy (HTTP 429)", resp.StatusCode)
		}
	}
	return obsFetchHTTPFailure(section, resp.StatusCode)
}

func obsFetchHTTPFailure(section string, status int) error {
	reason := "http_error"
	switch {
	case status == http.StatusUnauthorized:
		reason = "unauthorized"
	case status == http.StatusForbidden:
		reason = "forbidden"
	case status == http.StatusNotFound:
		reason = "endpoint_not_found"
	case status == http.StatusTooManyRequests:
		reason = "rate_limited"
	case status >= 300 && status < 400:
		reason = "redirect_rejected"
	case status >= 500 && status < 600:
		reason = "server_error"
	}
	return obsFetchFailed(section, reason, fmt.Sprintf("apid returned HTTP %d", status), status)
}

func obsFetchFailureDetails(err error) (string, *int) {
	var failure *obsFetchFailure
	if errors.As(err, &failure) && failure != nil {
		reason := "unavailable"
		switch failure.reason {
		case "invalid_url", "cancelled", "timeout", "transport_unavailable", "unauthorized", "forbidden", "endpoint_not_found", "rate_limited", obsFetchRuntimeBusyReason, "redirect_rejected", "server_error", "http_error", "response_read_failed", "response_too_large", "invalid_response":
			reason = failure.reason
		}
		if failure.status >= 100 && failure.status <= 999 {
			status := failure.status
			return reason, &status
		}
		return reason, nil
	}
	return obsFetchErrorReason(err, "unavailable"), nil
}
