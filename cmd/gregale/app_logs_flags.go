package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Accept the documented slug-first form as well as stdlib flags-first.
// Only move the leading slug; flag values and boolean flags retain their
// normal flag package semantics, including values starting with a dash.
func parseAppLogFlags(fs *flag.FlagSet, args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		reordered := append([]string(nil), args[1:]...)
		return fs.Parse(append(reordered, args[0]))
	}
	return fs.Parse(args)
}

func appLogsDegradedMessage(data string) string {
	var event struct {
		Code string `json:"code"`
	}
	if json.Unmarshal([]byte(data), &event) == nil && event.Code == "not_found" {
		return "No running instance is available for these logs; wait for deployment or wake the app."
	}
	return "Log stream degraded: the scheduler is temporarily unavailable"
}

// appLogsDegradedProblem converts the SSE degraded event (which arrives
// after the HTTP 200 has already started) into the same RFC 7807 shape used
// by the regular API error path. The stream has no response status to carry,
// so degraded logs are classified as a transient 503 and retain the event's
// not_found code when the app has no running instance.
func appLogsDegradedProblem(data string) api.Problem {
	var event struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(data), &event)
	code := api.CodeCapacity
	if event.Code == api.CodeNotFound {
		code = api.CodeNotFound
	}
	return api.Problem{
		Type:    "about:blank",
		Title:   "Logs unavailable",
		Status:  http.StatusServiceUnavailable,
		Code:    code,
		Detail:  appLogsDegradedMessage(data),
		DocsURL: docsURLForCode(code),
	}
}
