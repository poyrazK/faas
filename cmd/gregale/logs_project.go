package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectLogsRetry is how long a followed project log tail waits before it
// asks again for a service with no running instance (ADR-20261026190244).
const projectLogsRetry = 5 * time.Second

// projectLogsFlags are the `gregale logs` flags --project accepts.
var projectLogsFlags = map[string]bool{"project": true, "service": true, "follow": true, "since": true, "grep": true, "level": true, "json": true}

// projectLogsClient is the API surface a project log tail needs.
type projectLogsClient interface {
	GetProject(ctx context.Context, slug string) (api.ProjectResponse, error)
	StreamAppLogs(ctx context.Context, slug, deploymentID string, follow bool, opts api.LogFilter) (io.ReadCloser, error)
}

// cmdLogsProject runs `gregale logs --project`: every service of a project
// in one stream, each line prefixed with its service, as `docker compose
// logs` does.
func cmdLogsProject(fs *flag.FlagSet, project, services string, follow bool, since, grep, level string) int {
	var other []string
	fs.Visit(func(f *flag.Flag) {
		if !projectLogsFlags[f.Name] {
			other = append(other, "--"+f.Name)
		}
	})
	if len(other) > 0 || fs.NArg() > 0 {
		return printErr("Invalid project log flags", fmt.Errorf("--project reads runtime logs with --service, --follow, --since, --grep and --level only; remove %s",
			strings.Join(append(other, fs.Args()...), " ")))
	}
	if level != "" && !api.IsValidLogLevel(level) {
		return printErr("Invalid --level", errors.New("use info, warn or error"))
	}
	normalizedSince, err := normalizeLogsSince(since, logsSourceRuntime, time.Now())
	if err != nil {
		return printErr("Invalid --since", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runProjectLogs(ctx, client, project, splitProjectLogServices(services), follow,
		api.LogFilter{Grep: grep, Since: normalizedSince, Level: level}, osStdout, osStderr, projectLogsRetry)
}

func splitProjectLogServices(raw string) []string {
	var out []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// runProjectLogs streams the runtime logs of a project's services together.
// Each service streams on its own; with follow, a service without a running
// instance is asked again every retry interval, so its lines appear once it
// wakes. Lines are written whole, so services never interleave mid-line.
func runProjectLogs(ctx context.Context, client projectLogsClient, project string, only []string, follow bool, filter api.LogFilter, out, errOut io.Writer, retry time.Duration) int {
	resp, err := client.GetProject(ctx, project)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			renderAPIError(errOut, apiErr)
			return exitCodeForStatus(apiErr.Problem.Status)
		}
		return printErr("Could not load the project", err)
	}
	members := projectLogMembers(resp.Workloads, only)
	if len(members) == 0 {
		return printErr("No services to read", fmt.Errorf("project %s has no running services named %s", project, strings.Join(only, ", ")))
	}
	var mu sync.Mutex
	write := func(w io.Writer, service, line string) {
		mu.Lock()
		defer mu.Unlock()
		if jsonOutput {
			_, _ = fmt.Fprintln(w, projectLogJSON(service, line))
			return
		}
		_, _ = fmt.Fprintln(w, "["+service+"] "+line)
	}
	var wg sync.WaitGroup
	for _, member := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			streamProjectServiceLogs(ctx, client, member, follow, filter, retry, func(line string) { write(out, member.WorkloadName, line) },
				func(line string) { write(errOut, member.WorkloadName, line) })
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return 130
	}
	return 0
}

// projectLogMembers are the project's live services, narrowed to only when
// it names any (by service name, in any form: billing_api or billing-api).
func projectLogMembers(workloads []api.ProjectWorkloadResponse, only []string) []api.ProjectWorkloadResponse {
	var out []api.ProjectWorkloadResponse
	for _, w := range workloads {
		if w.Slug == "" || w.Status == "deleted" {
			continue
		}
		if len(only) > 0 && !containsServiceName(only, w.WorkloadName) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func containsServiceName(names []string, name string) bool {
	for _, candidate := range names {
		if api.SameServiceName(candidate, name) {
			return true
		}
	}
	return false
}

func streamProjectServiceLogs(ctx context.Context, client projectLogsClient, member api.ProjectWorkloadResponse, follow bool, filter api.LogFilter, retry time.Duration, line, note func(string)) {
	for {
		body, err := client.StreamAppLogs(ctx, member.Slug, "", follow, filter)
		parked := false
		switch {
		case err != nil:
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Problem.Status >= 400 && apiErr.Problem.Status < 500 {
				note("could not read logs: " + apiErr.Problem.Title)
				return
			}
			if ctx.Err() == nil {
				note("could not read logs: " + err.Error())
			}
		default:
			dec := api.NewDecoder(body)
			dec.SetCloseFn(body.Close)
			_, _ = consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
				switch e.Event {
				case "degraded":
					parked = isNoRunningInstanceLogs(e.Data)
					return true, 0
				case "end":
					return true, 0
				}
				if e.Data != "" {
					if jsonOutput {
						line(e.Data)
					} else {
						line(formatRuntimeLogLine(e.Data))
					}
				}
				return false, 0
			})
			_ = dec.Close()
		}
		if !follow || ctx.Err() != nil {
			if parked && !follow {
				note("parked: no running instance; read its retained logs with gregale logs " + member.Slug + " --since 1h")
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// projectLogJSON tags one line with its service for --json output: the line
// itself when it is a JSON object, else a {"line": ...} object.
func projectLogJSON(service, line string) string {
	var fields map[string]any
	if json.Unmarshal([]byte(line), &fields) != nil || fields == nil {
		fields = map[string]any{"line": line}
	}
	fields["service"] = service
	encoded, err := json.Marshal(fields)
	if err != nil {
		return line
	}
	return string(encoded)
}
