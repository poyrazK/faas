package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdStatusDispatch(args []string) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl status <incident|maintenance> <command> [flags]")
		return 2
	}
	kind, command := args[0], args[1]
	if kind != "incident" && kind != "maintenance" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl status: unknown event kind %q\n", kind)
		return 2
	}
	switch {
	case command == "list":
		return cmdStatusList(kind, args[2:])
	case kind == "incident" && command == "create":
		return cmdStatusCreateIncident(args[2:])
	case kind == "incident" && command == "update":
		return cmdStatusUpdate(args[2:], "", false)
	case kind == "incident" && command == "resolve":
		return cmdStatusUpdate(args[2:], "resolved", false)
	case kind == "maintenance" && command == "schedule":
		return cmdStatusScheduleMaintenance(args[2:])
	case kind == "maintenance" && command == "update":
		return cmdStatusUpdate(args[2:], "", true)
	case kind == "maintenance" && command == "start":
		return cmdStatusUpdate(args[2:], "in_progress", false)
	case kind == "maintenance" && command == "complete":
		return cmdStatusUpdate(args[2:], "completed", false)
	case kind == "maintenance" && command == "cancel":
		return cmdStatusUpdate(args[2:], "cancelled", false)
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl status %s: unknown command %q\n", kind, command)
		return 2
	}
}

func cmdStatusCreateIncident(args []string) int {
	fs := flag.NewFlagSet("status incident create", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	title := fs.String("title", "", "public incident title")
	impact := fs.String("impact", "", "degraded|partial_outage|major_outage")
	components := fs.String("components", "", "comma-separated public capabilities")
	stateValue := fs.String("state", "investigating", "investigating|identified|monitoring")
	message := fs.String("message", "", "public update message")
	starts := fs.String("starts-at", "", "RFC3339 start time (default now)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	startsAt := time.Now().UTC()
	if *starts != "" {
		parsed, err := time.Parse(time.RFC3339, *starts)
		if err != nil {
			return statusCLIError("incident create", "--starts-at must be RFC3339")
		}
		startsAt = parsed
	}
	request := api.AdminStatusEventCreateRequest{
		Kind: "incident", Title: *title, Impact: *impact, Components: splitStatusComponents(*components),
		State: *stateValue, StartsAt: &startsAt, Message: *message,
	}
	return statusCreate(request, "incident create")
}

func cmdStatusScheduleMaintenance(args []string) int {
	fs := flag.NewFlagSet("status maintenance schedule", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	title := fs.String("title", "", "public maintenance title")
	components := fs.String("components", "", "comma-separated public capabilities")
	message := fs.String("message", "", "public update message")
	startRaw := fs.String("start", "", "scheduled start in RFC3339")
	endRaw := fs.String("end", "", "scheduled end in RFC3339")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	start, err := time.Parse(time.RFC3339, *startRaw)
	if err != nil {
		return statusCLIError("maintenance schedule", "--start must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, *endRaw)
	if err != nil {
		return statusCLIError("maintenance schedule", "--end must be RFC3339")
	}
	request := api.AdminStatusEventCreateRequest{
		Kind: "maintenance", Title: *title, Impact: "maintenance", Components: splitStatusComponents(*components),
		State: "scheduled", ScheduledStartAt: &start, ScheduledEndAt: &end, Message: *message,
	}
	return statusCreate(request, "maintenance schedule")
}

func statusCreate(request api.AdminStatusEventCreateRequest, action string) int {
	client, err := statusOperatorClient()
	if err != nil {
		return statusCLIError(action, err.Error())
	}
	var event api.PublicStatusEvent
	if err := client.doJSON(context.Background(), http.MethodPost, "/v1/admin/status/incidents", request, &event, true, nil); err != nil {
		return statusCLIError(action, err.Error())
	}
	return printStatusMutation(event, client.baseURL)
}

func cmdStatusUpdate(args []string, forcedState string, preserveState bool) int {
	fs := flag.NewFlagSet("status update", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	id := fs.String("id", "", "public event UUID")
	stateValue := fs.String("state", forcedState, "new lifecycle state")
	message := fs.String("message", "", "public update message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *message == "" || (!preserveState && *stateValue == "") {
		return statusCLIError("update", "--id, --message, and a lifecycle state are required")
	}
	client, err := statusOperatorClient()
	if err != nil {
		return statusCLIError("update", err.Error())
	}
	if preserveState && *stateValue == "" {
		var current api.PublicStatusEvent
		if err := client.doJSON(context.Background(), http.MethodGet, "/v1/status/incidents/"+*id, nil, &current, false, nil); err != nil {
			return statusCLIError("update", err.Error())
		}
		*stateValue = current.State
	}
	var event api.PublicStatusEvent
	path := "/v1/admin/status/incidents/" + *id + "/updates"
	if err := client.doJSON(context.Background(), http.MethodPost, path, api.AdminStatusEventUpdateRequest{State: *stateValue, Message: *message}, &event, true, nil); err != nil {
		return statusCLIError("update", err.Error())
	}
	return printStatusMutation(event, client.baseURL)
}

func cmdStatusList(kind string, args []string) int {
	fs := flag.NewFlagSet("status list", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	active := fs.Bool("active", false, "only active events")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	client, err := statusOperatorClient()
	if err != nil {
		return statusCLIError("list", err.Error())
	}
	path := "/v1/admin/status/incidents?kind=" + kind
	if *active {
		path += "&active=true"
	}
	var events []api.PublicStatusEvent
	if err := client.doJSON(context.Background(), http.MethodGet, path, nil, &events, false, nil); err != nil {
		return statusCLIError("list", err.Error())
	}
	if jsonEnabled() {
		return emitOperatorJSON(events)
	}
	if len(events) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No status events.")
		return 0
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(osStdout, "%s  %-14s  %s  %s\n", event.ID, event.State, event.Title, statusPermalink(client.baseURL, event.ID))
	}
	return 0
}

func statusOperatorClient() (*operatorHTTPClient, error) {
	session, err := loadOperatorSession()
	if err != nil {
		return nil, err
	}
	return newOperatorHTTPClient(&session), nil
}

func printStatusMutation(event api.PublicStatusEvent, apiBaseURL string) int {
	if jsonEnabled() {
		return emitOperatorJSON(event)
	}
	_, _ = fmt.Fprintf(osStdout, "%s %s: %s\nPublic permalink: %s\n", event.Kind, event.State, event.Title, statusPermalink(apiBaseURL, event.ID))
	return 0
}

func statusPermalink(apiBaseURL, id string) string {
	parsed, err := url.Parse(apiBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(apiBaseURL, "/") + "/status/incidents/" + id
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Host = strings.TrimPrefix(parsed.Host, "api.")
	return strings.TrimRight(parsed.String(), "/") + "/status/incidents/" + id
}

func splitStatusComponents(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func statusCLIError(action, message string) int {
	_, _ = fmt.Fprintf(osStderr, "gregalectl status %s: %s\n", action, message)
	return 1
}
