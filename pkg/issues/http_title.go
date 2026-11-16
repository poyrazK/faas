package issues

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// httpIssueTitle names an error group the gateway recorded from responses:
// the status and where it happened. The gateway never reads the response
// body, so its error class is almost always the generic "unhandled" (5xx) or
// "client_error" (4xx) and its message is the request path, which read as
// "unhandled: /" (prod findings 2026-11-15 #37). A specific class is kept.
// The title is display only: the fingerprint does not change.
func httpIssueTitle(in api.IssueEvent) string {
	title := fmt.Sprintf("HTTP %d", in.HTTPStatus)
	if text := http.StatusText(in.HTTPStatus); text != "" {
		title += " " + text
	}
	where := in.Route
	if where == "" {
		where = in.Message
	}
	if where != "" {
		title += " on " + where
	}
	switch in.ExceptionType {
	case "", "unhandled", "client_error":
	default:
		title += " (" + in.ExceptionType + ")"
	}
	return title
}
