package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type logQueryPreviewView struct {
	Name           string          `json:"name"`
	Path           string          `json:"path"`
	AppSource      string          `json:"-"`
	Overrides      map[string]bool `json:"-"`
	ClearedFilters []string        `json:"cleared_filters,omitempty"`
	DefaultsPath   string          `json:"defaults_path,omitempty"`
}

// A preview must not fall through to the interactive app picker.
func resolveLogsAppTarget(explicit string, preview bool) (string, error) {
	if !preview {
		return resolveReadAppTarget(explicit)
	}
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	linked, _, err := linkedProjectContext(cwd)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return "", err
	}
	if err == nil && linked.App != "" {
		return linked.App, nil
	}
	return "", fmt.Errorf("%w; pass --app SLUG or link a project with --app; preview does not open a picker", errReadAppTargetRequired)
}

func logPreviewAppSource(fs *flag.FlagSet) string {
	if logsFlagWasSet(fs, "app") && fs.Lookup("app").Value.String() != "" {
		return "explicit_flag"
	}
	if fs.NArg() > 0 {
		return "explicit_name"
	}
	return "linked_project"
}

func previewSelectedLogQuery(fs *flag.FlagSet, slug, mode string, view *logQueryPreviewView) int {
	if !api.ValidAppSlug(slug) {
		return printErr("Invalid app target", errors.New("provide a valid app slug"))
	}
	if mode == "latest_failure" {
		return previewLogQuery(fs, slug, "build/deploy", "", nil, mode, view)
	}
	since := fs.Lookup("since").Value.String()
	if since == "" {
		since = "15m"
	}
	normalized, err := normalizeLogsSince(since, logsSourceHTTP, time.Now())
	if err != nil {
		return printErr("Invalid request error window", err)
	}
	return previewLogQuery(fs, slug, logsSourceHTTP, normalized, nil, mode, view)
}

func previewLogQuery(fs *flag.FlagSet, slug, source, since string, archive *api.ArchiveLogSelector, mode string, view *logQueryPreviewView) int {
	filters := map[string]any{}
	sources := map[string]string{"app": "", "source": "cli_default"}
	sources["app"] = logPreviewAppSource(fs)
	if view != nil {
		sources["app"] = view.AppSource
		sources["view"] = "explicit_flag"
		if view.DefaultsPath != "" {
			sources["view"] = "project_defaults"
		}
	}
	flagSource := func(name string) string {
		if view != nil && view.Overrides[name] {
			return "explicit_flag"
		}
		if logsFlagWasSet(fs, name) {
			if view != nil && name != "app" && name != "dry-run" && !view.Overrides[name] {
				return "saved_view"
			}
			return "explicit_flag"
		}
		return "cli_default"
	}
	add := func(name string) {
		filters[name] = fs.Lookup(name).Value.(flag.Getter).Get()
		sources[name] = flagSource(name)
	}
	notes := []string{
		"Offline preview only; no authentication, log reads, remote selections, or file writes were performed.",
		"Logs are app-scoped; a linked environment does not filter this query.",
	}
	switch {
	case mode == "latest_failure":
		filters["max_pages"], filters["page_size"], filters["entry_limit"] = 50, 10, 500
		sources["source"] = "implied_latest_failure"
		notes = append(notes, "Latest failed deployment and retained build/deploy entries remain unresolved; no runtime fallback is selected.")
	case mode == "latest_request_error":
		add("since")
		add("route")
		filters["since"] = since
		filters["status_range"], filters["max_pages"], filters["page_size"] = "500..599", 50, 200
		sources["source"] = "implied_latest_request_error"
		if view != nil {
			sources["source"] = "saved_view"
		}
		notes = append(notes, "Newest retained HTTP 5xx request, server retention, and coverage remain unresolved.")
	case archive != nil:
		add("instance")
		add("date")
		add("explain")
		source = "runtime_archive"
		sources["source"] = "implied_archive"
		notes = append(notes, "Archived instance and UTC day have not been verified remotely.")
	default:
		sources["source"] = flagSource("source")
		if fs.Lookup("source").Value.String() == "" && source == logsSourceHTTP {
			sources["source"] = "inferred_http_filters"
		}
		add("since")
		filters["since"] = since
		add("deployment")
		if logsFlagWasSet(fs, "release") {
			sources["deployment"] = flagSource("release")
		}
		if source == logsSourceHTTP {
			for _, name := range []string{"status", "route", "request", "trace"} {
				add(name)
			}
			filters["request"] = strings.TrimSpace(fs.Lookup("request").Value.String())
			filters["trace"] = strings.TrimSpace(fs.Lookup("trace").Value.String())
			if filters["request"] == "" {
				add("limit")
				add("all")
			}
			if filters["trace"] != "" {
				filters["trace_lookup_limit"] = traceLogsLookupLimit
			}
		} else {
			for _, name := range []string{"grep", "level", "follow", "explain"} {
				add(name)
			}
		}
		notes = append(notes, "Deployment references (including vN/latest), server defaults, retained coverage, and runtime/build-log routing have not been resolved.")
	}
	for key := range filters {
		if sources[key] == "" {
			sources[key] = "selection_rule"
		}
	}
	if mode != "query" {
		sources["selection"] = flagSource(strings.ReplaceAll(mode, "_", "-"))
	}
	connection := configuredPreviewConnection()
	report := struct {
		DryRun           bool                 `json:"dry_run"`
		App              string               `json:"app"`
		Profile          string               `json:"profile"`
		EnvironmentScope string               `json:"environment_scope"`
		Mode             string               `json:"mode"`
		Source           string               `json:"source"`
		RequestedSince   string               `json:"requested_since,omitempty"`
		View             *logQueryPreviewView `json:"view,omitempty"`
		Filters          map[string]any       `json:"filters"`
		SettingSources   map[string]string    `json:"setting_sources"`
		Notes            []string             `json:"notes"`
		QueryCommand     string               `json:"query_command"`
		Connection       previewConnection    `json:"connection"`
	}{true, slug, connection.Profile, "app-scoped; linked environment is not a filter", mode, source, fs.Lookup("since").Value.String(), view, filters, sources, notes, logQueryPreviewCommand(fs, slug, source, mode, archive), connection}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Log query preview (offline)\nApp: %s (source=%s)\nSource: %s (source=%s)\nMode: %s\n", oneLine(slug), sources["app"], source, sources["source"], mode)
	renderPreviewConnection(report.Connection)
	if view != nil {
		_, _ = fmt.Fprintf(osStdout, "Saved view: %s\nView file: %s\n", oneLine(view.Name), oneLine(view.Path))
		if view.DefaultsPath != "" {
			_, _ = fmt.Fprintf(osStdout, "View selected from project defaults: %s\n", oneLine(view.DefaultsPath))
		}
	}
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		origin := sources[key]
		if origin == "" {
			origin = "selection_rule"
		}
		value := oneLine(fmt.Sprint(filters[key]))
		if key == "status" && view != nil && view.Overrides["clear-status"] {
			value = "all HTTP statuses (saved filter cleared)"
		} else if view != nil && view.Overrides["clear-"+key] {
			value = "not restricted (saved filter cleared)"
		}
		_, _ = fmt.Fprintf(osStdout, "  %s: %s (source=%s)\n", key, value, origin)
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(osStdout, note)
	}
	_, _ = fmt.Fprintf(osStdout, "Run this query (POSIX shells; not executed):\n  %s\n", report.QueryCommand)
	return 0
}

// Reconstruct the effective query, without view/clear/preview flags. Preserve
// relative windows so copying a preview does not freeze its wall-clock cutoff.
func logQueryPreviewCommand(fs *flag.FlagSet, slug, source, mode string, archive *api.ArchiveLogSelector) string {
	args := []string{"logs", "--app", slug}
	value := func(name string) string { return fs.Lookup(name).Value.String() }
	addText := func(name string) {
		text := value(name)
		if name == "request" || name == "trace" {
			text = strings.TrimSpace(text)
		}
		if text != "" {
			args = append(args, "--"+name, text)
		}
	}
	addBool := func(name string) {
		if fs.Lookup(name).Value.(flag.Getter).Get().(bool) {
			args = append(args, "--"+name)
		}
	}
	switch {
	case mode == "latest_failure":
		args = append(args, "--latest-failure")
	case mode == "latest_request_error":
		window := value("since")
		if window == "" {
			window = "15m"
		}
		args = append(args, "--latest-request-error", "--since", window)
		addText("route")
	case archive != nil:
		args = append(args, "--archive", "--instance", archive.InstanceID, "--date", archive.Date)
		addBool("explain")
	default:
		args = append(args, "--source", source)
		addText("since")
		addText("deployment")
		if source == logsSourceHTTP {
			for _, name := range []string{"route", "request", "trace"} {
				addText(name)
			}
			if fs.Lookup("status").Value.(flag.Getter).Get().(int) != 0 {
				args = append(args, "--status", value("status"))
			}
			if strings.TrimSpace(value("request")) == "" {
				args = append(args, "--limit", value("limit"))
				addBool("all")
			}
		} else {
			addText("grep")
			addText("level")
			addBool("follow")
			addBool("explain")
		}
	}
	return savedNameSuggestionCommand(args)
}
