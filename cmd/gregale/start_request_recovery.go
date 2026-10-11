package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Failure details are ephemeral. Retrying a GET never changes the accepted
// deployment, source, access settings, or durable session metadata.
type startRequestFailure struct {
	httpStatus  int
	logsAllowed bool
}

func startHTTPFailureHint(status int) string {
	switch {
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return "This GET path does not match an available route. Check another app-relative path or inspect the app logs."
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "This endpoint denied access. Check the app's authentication settings before retrying."
	case status == http.StatusTooManyRequests:
		return "The app is limiting requests. Give it a moment, then retry this check."
	case status >= 300 && status < 400:
		return "This endpoint redirected the request. Check its routing or authentication settings before retrying."
	case status >= 500:
		return "The app or gateway could not serve this request. Check the recent app logs, then retry this check."
	default:
		return "The app rejected this request. Check its response and logs before retrying."
	}
}

func (r *startRunner) checkFirstResponse(path, expectedGreeting string) int {
	code := r.sendTestRequest(path, expectedGreeting)
	for code != 0 && code != 130 {
		failure := r.requestFailure
		if failure == nil {
			return code
		}
		if failure.logsAllowed {
			r.showRequestLogs(5)
		}
		for {
			if r.ctx.Err() != nil {
				return 130
			}
			choices := []string{"Retry this GET check"}
			pathIndex, logsIndex := -1, -1
			if expectedGreeting == "" && (failure.httpStatus == http.StatusNotFound || failure.httpStatus == http.StatusMethodNotAllowed) {
				pathIndex = len(choices)
				choices = append(choices, "Check another GET path")
			}
			if failure.logsAllowed {
				logsIndex = len(choices)
				choices = append(choices, "Show recent app logs")
			}
			finishIndex := len(choices)
			choices = append(choices, "Finish")
			choice, err := r.prompt.choose(r.ctx, "Your deployment is saved. Let's check its response.", choices, 0)
			if err != nil {
				return r.requestInputExit(err, code)
			}
			if choice == finishIndex {
				return code
			}
			if choice == logsIndex {
				r.showRequestLogs(20)
				continue
			}
			if choice == pathIndex {
				path, err = r.requestPath(path)
				if err != nil {
					return r.requestInputExit(err, code)
				}
			}
			PrintProgress(osStdout, "Checking GET %s on the saved deployment...", path)
			code = r.sendTestRequest(path, expectedGreeting)
			break
		}
	}
	return code
}

func (r *startRunner) requestInputExit(err error, failureCode int) int {
	if r.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, io.EOF) {
		return failureCode // Closing an unresolved check must not report success.
	}
	return startInputExit(err)
}

func (r *startRunner) requestPath(previous string) (string, error) {
	for {
		path, err := r.prompt.text(r.ctx, "GET path", previous)
		if err != nil {
			return "", err
		}
		if validStartRequestPath(path) {
			return path, nil
		}
		PrintWarn(osStdout, "Use an app-relative path such as / or /healthz.")
	}
}

func (r *startRunner) showRequestLogs(limit int) {
	ctx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
	defer cancel()
	deployment := ""
	if api.Plan(r.account.Plan).LogDeploymentFilterMax() > 0 {
		deployment = r.session.DeploymentID
	}
	body, err := r.client.StreamAppLogs(ctx, r.session.AppSlug, deployment, false, api.LogFilter{})
	if err != nil {
		PrintProgress(osStdout, "Recent app logs are unavailable. You can still retry the check.")
		return
	}
	defer func() { _ = body.Close() }()
	lines := readStartRequestLogs(body, r.session.DeploymentID, limit)
	if len(lines) == 0 {
		PrintProgress(osStdout, "No recent app logs were available for this check.")
		return
	}
	clean := r.requestLogCleaner()
	_, _ = fmt.Fprintln(osStdout, "\nRecent app logs")
	for _, line := range lines {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", clean(line))
	}
}

// Read a bounded, non-following page without a parser goroutine that could
// outlive cancellation. Only log frames are shown; identity-bearing frames
// from another deployment are omitted even on plans without server filtering.
func readStartRequestLogs(body io.Reader, deployment string, limit int) []string {
	scanner := bufio.NewScanner(io.LimitReader(body, 64<<10))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	var lines, data []string
	event := ""
	flush := func() {
		var entry struct {
			Line         string `json:"line"`
			DeploymentID string `json:"deployment_id"`
		}
		if (event == "log" || event == "") && json.Unmarshal([]byte(strings.Join(data, "\n")), &entry) == nil && entry.Line != "" && (entry.DeploymentID == "" || entry.DeploymentID == deployment) {
			lines = append(lines, entry.Line)
			if len(lines) > limit {
				lines = lines[1:]
			}
		}
		data, event = nil, ""
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimPrefix(strings.TrimPrefix(line, "event:"), " ")
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return lines
}

func (r *startRunner) requestLogCleaner() func(string) string {
	values := []string{loadToken()}
	if r.secretsFile != "" {
		if pairs, err := readSecretsFile(r.secretsFile); err == nil {
			for _, pair := range pairs {
				values = append(values, pair.Value)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	var replacements []string
	for _, value := range values {
		if value != "" {
			replacements = append(replacements, value, "[REDACTED]")
			encoded, _ := json.Marshal(value)
			if escaped := string(encoded[1 : len(encoded)-1]); escaped != value {
				replacements = append(replacements, escaped, "[REDACTED]")
			}
		}
	}
	known := strings.NewReplacer(replacements...)
	patterns := redact.New(4096)
	return func(line string) string {
		line = known.Replace(line)
		line, _ = patterns.Apply(line)
		line = strings.Join(strings.Fields(startResponseText(line)), " ")
		return safetext.Truncate(line, 512)
	}
}
