package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

type logView struct {
	LatestRequestError bool   `json:"latest_request_error,omitempty"`
	Source             string `json:"source"`
	Since              string `json:"since"`
	Level              string `json:"level,omitempty"`
	Grep               string `json:"grep,omitempty"`
	Follow             bool   `json:"follow,omitempty"`
	Status             int    `json:"status,omitempty"`
	Route              string `json:"route,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

type logViewsFile struct {
	Version int                `json:"version"`
	Views   map[string]logView `json:"views"`
}

func (v logView) args() []string {
	if v.LatestRequestError {
		args := []string{"--latest-request-error", "--since", v.Since}
		if v.Route != "" {
			args = append(args, "--route", v.Route)
		}
		return args
	}
	args := []string{"--source", v.Source, "--since", v.Since}
	for _, pair := range [][2]string{{"--level", v.Level}, {"--grep", v.Grep}, {"--route", v.Route}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	if v.Follow {
		args = append(args, "--follow")
	}
	if v.Status != 0 {
		args = append(args, "--status", strconv.Itoa(v.Status))
	}
	if v.Source == logsSourceHTTP {
		args = append(args, "--limit", strconv.Itoa(v.Limit))
	}
	return args
}

func validateLogView(v logView) error {
	if v.Source != logsSourceRuntime && v.Source != logsSourceHTTP {
		return errors.New("source must be runtime or http")
	}
	if _, err := parsePositiveLogsDuration(v.Since); err != nil {
		return errors.New("saved views require a positive relative window such as 15m, 1h, or 3d")
	}
	if v.Level != "" && !api.IsValidLogLevel(v.Level) {
		return errors.New("invalid log level")
	}
	for _, text := range []string{v.Grep, v.Route} {
		if len(text) > 2048 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return errors.New("filters must be at most 2048 bytes without control characters")
		}
	}
	if v.LatestRequestError {
		if v.Source != logsSourceHTTP || v.Level != "" || v.Grep != "" || v.Follow || v.Status != 0 || v.Limit != 0 {
			return errors.New("latest request error views accept only an HTTP source, relative window, and optional route")
		}
		return nil
	}
	if v.Source == logsSourceRuntime && (v.Status != 0 || v.Route != "" || v.Limit != 0) {
		return errors.New("HTTP filters require source http")
	}
	if v.Source == logsSourceHTTP && (v.Level != "" || v.Grep != "" || v.Follow || v.Limit < 1 || v.Limit > 200 || v.Status != 0 && (v.Status < 100 || v.Status > 599)) {
		return errors.New("HTTP views accept status 100..599, route, and limit 1..200; runtime filters are incompatible")
	}
	return nil
}

func parseLogViewFlags(args []string) (logView, bool, error) {
	view, replace, _, err := parseLogViewSaveFlags(args)
	return view, replace, err
}

func parseLogViewSaveFlags(args []string) (logView, bool, bool, error) {
	fs := newFlagSet("logs views save", flag.ContinueOnError)
	v := logView{}
	fs.BoolVar(&v.LatestRequestError, "latest-request-error", false, "select newest recorded HTTP 5xx row using the saved window and route")
	fs.StringVar(&v.Source, "source", logsSourceRuntime, "runtime or http")
	fs.StringVar(&v.Since, "since", "15m", "relative lookback")
	fs.StringVar(&v.Level, "level", "", "runtime log level")
	fs.StringVar(&v.Grep, "grep", "", "runtime text filter")
	fs.BoolVar(&v.Follow, "follow", false, "follow runtime logs")
	fs.IntVar(&v.Status, "status", 0, "exact HTTP status")
	fs.StringVar(&v.Route, "route", "", "HTTP route")
	fs.IntVar(&v.Limit, "limit", 100, "HTTP page size")
	replace := fs.Bool("replace", false, "replace an existing view")
	dryRun := fs.Bool("dry-run", false, "preview saved filters without writing or querying logs")
	if err := fs.Parse(args); err != nil {
		return v, false, false, err
	}
	if fs.NArg() != 0 {
		return v, false, false, errors.New("unexpected arguments in log view")
	}
	if v.LatestRequestError {
		if logsFlagWasSet(fs, "limit") || logsFlagWasSet(fs, "status") || logsFlagWasSet(fs, "level") || logsFlagWasSet(fs, "grep") || logsFlagWasSet(fs, "follow") {
			return v, false, false, errors.New("latest request error views accept only --since, --route, an optional --source http, --replace, and --dry-run")
		}
		if !logsFlagWasSet(fs, "source") {
			v.Source = logsSourceHTTP
		}
		v.Limit = 0
	}
	if v.Source == logsSourceRuntime && !logsFlagWasSet(fs, "limit") {
		v.Limit = 0
	}
	return v, *replace, *dryRun, validateLogView(v)
}

func logViewsPath() (string, error) {
	path, err := cliConfigPath()
	return filepath.Join(filepath.Dir(path), "log-views.json"), err
}

func loadLogViews() (logViewsFile, error) {
	views := logViewsFile{Version: 1, Views: map[string]logView{}}
	path, err := logViewsPath()
	if err != nil {
		return views, err
	}
	file, err := openCustomerFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return views, nil
	}
	if err != nil {
		return views, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return views, err
	}
	if info.Size() > 1<<20 {
		return views, errors.New("saved log views exceed 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&views); err != nil {
		return views, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return views, errors.New("invalid trailing content in log views")
	}
	if views.Version != 1 || views.Views == nil {
		return views, errors.New("unsupported log views format")
	}
	for name, view := range views.Views {
		if err := validateProfileName(name); err != nil {
			return views, errors.New("invalid saved view name")
		}
		if err := validateLogView(view); err != nil {
			return views, fmt.Errorf("invalid view %s: %w", name, err)
		}
	}
	return views, nil
}

func writeLogViews(views logViewsFile) error {
	path, err := logViewsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return err
	}
	if len(body) >= 1<<20 {
		return errors.New("saved log views exceed 1 MiB")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".log-views-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(body, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func saveLogView(name string, view logView, replace bool) error {
	if err := validateProfileName(name); err != nil {
		return err
	}
	if err := validateLogView(view); err != nil {
		return err
	}
	views, err := loadLogViews()
	if err != nil {
		return err
	}
	if _, exists := views.Views[name]; exists && !replace {
		return errors.New("view already exists; choose a new name or use --replace")
	}
	views.Views[name] = view
	return writeLogViews(views)
}

func cmdLogViews(args []string) int {
	if len(args) > 0 && args[0] == "delete" {
		return cmdLogViewDelete(args[1:])
	}
	if len(args) > 0 && args[0] == "copy" {
		return cmdLogViewCopy(args[1:])
	}
	if len(args) > 0 && args[0] == "rename" {
		return cmdLogViewRename(args[1:])
	}
	if len(args) > 0 && args[0] == "update" {
		return cmdLogViewUpdate(args[1:])
	}
	if len(args) == 0 {
		args = []string{"list"}
	} else if strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	if args[0] == "save" && len(args) >= 2 {
		view, replace, dryRun, err := parseLogViewSaveFlags(args[2:])
		if err != nil {
			return printErr("Invalid log view", err)
		}
		if dryRun {
			return previewSaveLogView(args[1], view, replace)
		}
		if err := saveLogView(args[1], view, replace); err != nil {
			return printErr("Could not save log view", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"name": args[1], "view": view}))
		}
		PrintOK(osStdout, "Saved log view %s", args[1])
		return 0
	}
	views, err := loadLogViews()
	if err != nil {
		return printErr("Could not read log views", err)
	}
	if args[0] == "list" {
		fs := newFlagSet("logs views list", flag.ContinueOnError)
		details := fs.Bool("details", false, "include saved filters and query/preview commands in JSON entries")
		app := fs.String("app", "", "include this explicit, unverified app target in query/preview commands")
		source := fs.String("source", "", "list only saved runtime or HTTP views")
		nameFilter := fs.String("name", "", "list saved views whose names contain this literal, case-sensitive text")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
			return 1
		}
		if logsFlagWasSet(fs, "app") && !api.ValidAppSlug(*app) {
			return printErr("Invalid app target", errors.New("--app requires a valid nonempty app slug; app existence and access are not checked by this listing"))
		}
		sourceSet := logsFlagWasSet(fs, "source")
		if sourceSet && *source != logsSourceRuntime && *source != logsSourceHTTP {
			return printErr("Invalid log view source filter", errors.New("--source requires runtime or http; omit the flag to list every saved view"))
		}
		nameSet := logsFlagWasSet(fs, "name")
		if nameSet && strings.TrimSpace(*nameFilter) == "" {
			return printErr("Invalid log view name filter", errors.New("--name requires nonempty literal text; omit it to list without a name filter"))
		}
		names := make([]string, 0, len(views.Views))
		for name, view := range views.Views {
			if (!sourceSet || view.Source == *source) && (!nameSet || strings.Contains(name, *nameFilter)) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		// Plain JSON remains a name-only listing and needs no preference read.
		if jsonOutput && !*details {
			return jsonOut(writeJSON(names))
		}
		preferred, _, preferenceErr := readLogDefaults()
		preferenceKnown := preferenceErr == nil || errors.Is(preferenceErr, os.ErrNotExist)
		if jsonOutput {
			rows := make([]preferredLogViewListing, 0, len(names))
			for _, name := range names {
				var selected *bool
				if preferenceKnown {
					value := preferenceErr == nil && preferred.View == name
					selected = &value
				}
				rows = append(rows, preferredLogViewListing{savedLogViewListingEntry(name, views.Views[name], *app), selected})
			}
			return jsonOut(writeJSON(rows))
		}
		if !preferenceKnown {
			_, _ = fmt.Fprintln(osStdout, "Preferred view: unknown (local preference could not be safely read). Inspect with logs defaults.")
		} else if preferenceErr == nil {
			if _, exists := views.Views[preferred.View]; !exists {
				_, _ = fmt.Fprintf(osStdout, "Preferred view: %s (not in the saved-view store). Inspect with logs defaults.\n", preferred.View)
			}
		}
		if sourceSet {
			_, _ = fmt.Fprintf(osStdout, "Saved view filter: source=%s; matching views=%d (local comparison only).\n", *source, len(names))
		}
		if nameSet {
			_, _ = fmt.Fprintf(osStdout, "Saved view name filter: contains=%q; matching views=%d (literal, case-sensitive comparison).\n", *nameFilter, len(names))
		}
		for _, name := range names {
			renderSavedLogViewEntry(name, views.Views[name], *app)
			if preferenceErr == nil && preferred.View == name {
				_, _ = fmt.Fprintln(osStdout, "  Preferred for logs --saved-view.")
			}
		}
		if len(names) > 0 {
			if *app != "" {
				_, _ = fmt.Fprintf(osStdout, "Commands target app %s explicitly (unverified). Relative windows are recalculated on each run.\n", oneLine(*app))
			} else {
				_, _ = fmt.Fprintln(osStdout, "Run commands use the linked app; add --app APP_SLUG to select an app explicitly. Relative windows are recalculated on each run.")
			}
			_, _ = fmt.Fprintln(osStdout, "Preview commands are offline; app access and retained logs are not checked.")
		}
		if len(names) == 0 {
			if nameSet {
				_, _ = fmt.Fprintln(osStdout, "No saved log views match the selected name/source filters. Omit --name and --source to list every saved view.")
			} else if sourceSet {
				_, _ = fmt.Fprintf(osStdout, "No saved log views match source %s. Omit --source to list every saved view.\n", *source)
			} else {
				_, _ = fmt.Fprintln(osStdout, "No saved log views. Save filters with logs views save NAME or logs --interactive.")
			}
		}
		return 0
	}
	if args[0] != "show" {
		return printErr("Invalid log views usage", errors.New("use logs views list, show NAME, save NAME [filters], update NAME [filters], delete NAME [--dry-run], or rename/copy OLD NEW"))
	}
	fs := newFlagSet("logs views show", flag.ContinueOnError)
	app := fs.String("app", "", "target generated query/preview commands at this unverified app")
	details := fs.Bool("details", false, "include saved filters and query/preview commands in JSON output")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		return printErr("Invalid log view show arguments", errors.New("use logs views show NAME [--app SLUG] [--details]"))
	}
	if logsFlagWasSet(fs, "app") && !api.ValidAppSlug(*app) {
		return printErr("Invalid app target", errors.New("--app requires a valid nonempty app slug; app existence and access are not checked by this command"))
	}
	name := fs.Arg(0)
	view, exists := views.Views[name]
	if !exists {
		return unknownSavedName("Unknown log view", name, views.Views, func(candidate string) []string {
			return correctedSavedNameFlags([]string{"logs", "views", "show"}, fs, candidate, "")
		}, []string{"logs", "views", "list"})
	}
	if jsonOutput {
		if *details {
			return jsonOut(writeJSON(savedLogViewListingEntry(name, view, *app)))
		}
		return jsonOut(writeJSON(view))
	}
	renderSavedLogViewEntry(name, view, *app)
	if *app != "" {
		_, _ = fmt.Fprintf(osStdout, "Commands target app %s explicitly (unverified).\n", oneLine(*app))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Commands use the linked app; add --app APP_SLUG to target an app explicitly.")
	}
	_, _ = fmt.Fprintln(osStdout, "Relative windows are recalculated on each run. Preview is offline; app access and retained logs are not checked.")
	return 0
}

type savedLogViewListing struct {
	Name           string  `json:"name"`
	View           logView `json:"view"`
	RunCommand     string  `json:"run_command"`
	PreviewCommand string  `json:"preview_command"`
	App            string  `json:"app,omitempty"`
	AppVerified    bool    `json:"app_verified"`
}

type preferredLogViewListing struct {
	savedLogViewListing
	Preferred *bool `json:"preferred"`
}

func savedLogViewListingEntry(name string, view logView, app string) savedLogViewListing {
	args := []string{"logs", "--view", name}
	if app != "" {
		args = append(args, "--app", app)
	}
	command := savedNameSuggestionCommand(args)
	return savedLogViewListing{Name: name, View: view, RunCommand: command, PreviewCommand: command + " --dry-run", App: app}
}

func savedLogViewFilterSummary(view logView) string {
	filters := []string{"since=" + view.Since}
	for _, field := range [][2]string{{"route", view.Route}, {"level", view.Level}, {"grep", view.Grep}} {
		if field[1] != "" {
			filters = append(filters, field[0]+"="+strconv.Quote(field[1]))
		}
	}
	if view.Status != 0 {
		filters = append(filters, "status="+strconv.Itoa(view.Status))
	}
	if view.Follow {
		filters = append(filters, "follow=true")
	}
	if view.Source == logsSourceHTTP && !view.LatestRequestError {
		filters = append(filters, "limit="+strconv.Itoa(view.Limit))
	}
	return strings.Join(filters, "; ")
}

func renderSavedLogViewEntry(name string, view logView, app string) {
	purpose := "runtime logs"
	if view.Source == logsSourceHTTP {
		purpose = "HTTP requests"
	}
	if view.LatestRequestError {
		purpose = "latest HTTP error"
	}
	entry := savedLogViewListingEntry(name, view, app)
	_, _ = fmt.Fprintf(osStdout, "%s  source=%s  since=%s\n  Purpose: %s\n  Filters: %s\n  Run (POSIX shells):\n    %s\n  Preview (POSIX shells):\n    %s\n", name, view.Source, view.Since, purpose, savedLogViewFilterSummary(view), entry.RunCommand, entry.PreviewCommand)
}
