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

func appLogsDegradedProblem(data string) api.Problem {
	return *api.NewProblem(http.StatusServiceUnavailable, "app_logs_unavailable",
		"App logs unavailable", appLogsDegradedMessage(data)).WithDocs(cliDocsURL)
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
