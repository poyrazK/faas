package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		return "No running instance is available for these logs; wake the app (`gregale wake <slug>`) or read a parked instance's retained logs with `gregale logs <slug> --archive --instance <id> --date YYYY-MM-DD` (instance ids: `gregale ps <slug> --all`)."
	}
	return "Log stream degraded: the scheduler is temporarily unavailable"
}

// appLogsArchiveEndReason decodes the terminal reason emitted by the
// durable-log archive stream. A missing or malformed reason is treated as a
// degraded archive so the CLI never reports success for an incomplete stream.
func appLogsArchiveEndReason(data string) string {
	var event struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(data), &event) != nil || event.Reason == "" {
		return "archive_degraded"
	}
	return event.Reason
}

func appLogsArchiveProblem(reason string) api.Problem {
	if reason == "archive_missing" {
		return *api.NewProblem(http.StatusNotFound, "log_archive_missing",
			"Archived logs not found",
			"No archived log object exists for the requested instance and UTC day; verify the instance and date, or check archive retention.").WithDocs(cliDocsURL)
	}
	return *api.NewProblem(http.StatusServiceUnavailable, "log_archive_degraded",
		"Archived logs unavailable",
		"The archive backend did not return a complete log archive; retry later or ask the operator to check it.").WithDocs(cliDocsURL)
}

func appLogsArchiveMessage(reason string) string {
	if reason == "archive_missing" {
		return "No archived logs were found for that instance and UTC day (archive gap); verify the instance and date or check archive retention."
	}
	return "Archived logs are temporarily unavailable; retry later or ask the operator to check the archive backend."
}

// formatRuntimeLogLine renders one runtime log event for a terminal. The
// stream carries a JSON envelope per line; printing it verbatim made human
// output identical to --json and kept the guest's trailing carriage return.
// Anything that is not the expected envelope is printed unchanged.
func formatRuntimeLogLine(data string) string {
	var event struct {
		Instance  string  `json:"instance"`
		Level     string  `json:"level"`
		Line      *string `json:"line"`
		Stream    string  `json:"stream"`
		WrittenAt string  `json:"written_at"`
	}
	if json.Unmarshal([]byte(data), &event) != nil || event.Line == nil {
		return data
	}
	stamp := event.WrittenAt
	if t, err := time.Parse(time.RFC3339Nano, event.WrittenAt); err == nil {
		stamp = t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	instance := event.Instance
	if len(instance) > 8 {
		instance = instance[:8]
	}
	prefix := strings.TrimSpace(fmt.Sprintf("%s %s %-6s", stamp, instance, event.Stream))
	if event.Level != "" {
		prefix += " " + strings.ToUpper(event.Level)
	}
	return prefix + " " + strings.TrimRight(*event.Line, "\r\n")
}
