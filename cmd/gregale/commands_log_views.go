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
	Source string `json:"source"`
	Since  string `json:"since"`
	Level  string `json:"level,omitempty"`
	Grep   string `json:"grep,omitempty"`
	Follow bool   `json:"follow,omitempty"`
	Status int    `json:"status,omitempty"`
	Route  string `json:"route,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type logViewsFile struct {
	Version int                `json:"version"`
	Views   map[string]logView `json:"views"`
}

func (v logView) args() []string {
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
	if v.Source == logsSourceRuntime && (v.Status != 0 || v.Route != "" || v.Limit != 0) {
		return errors.New("HTTP filters require source http")
	}
	if v.Source == logsSourceHTTP && (v.Level != "" || v.Grep != "" || v.Follow || v.Limit < 1 || v.Limit > 200 || v.Status != 0 && (v.Status < 100 || v.Status > 599)) {
		return errors.New("HTTP views accept status 100..599, route, and limit 1..200; runtime filters are incompatible")
	}
	return nil
}

func parseLogViewFlags(args []string) (logView, bool, error) {
	fs := newFlagSet("logs views save", flag.ContinueOnError)
	v := logView{}
	fs.StringVar(&v.Source, "source", logsSourceRuntime, "runtime or http")
	fs.StringVar(&v.Since, "since", "15m", "relative lookback")
	fs.StringVar(&v.Level, "level", "", "runtime log level")
	fs.StringVar(&v.Grep, "grep", "", "runtime text filter")
	fs.BoolVar(&v.Follow, "follow", false, "follow runtime logs")
	fs.IntVar(&v.Status, "status", 0, "exact HTTP status")
	fs.StringVar(&v.Route, "route", "", "HTTP route")
	fs.IntVar(&v.Limit, "limit", 100, "HTTP page size")
	replace := fs.Bool("replace", false, "replace an existing view")
	if err := fs.Parse(args); err != nil {
		return v, false, err
	}
	if fs.NArg() != 0 {
		return v, false, errors.New("unexpected arguments in log view")
	}
	if v.Source == logsSourceRuntime && !logsFlagWasSet(fs, "limit") {
		v.Limit = 0
	}
	return v, *replace, validateLogView(v)
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
	defer file.Close()
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
	defer os.Remove(file.Name())
	defer file.Close()
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
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale logs views <list|show NAME|save NAME [filters]|delete NAME>", "logs")
		return 1
	}
	if args[0] == "save" && len(args) >= 2 {
		view, replace, err := parseLogViewFlags(args[2:])
		if err != nil {
			return printErr("Invalid log view", err)
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
	if args[0] == "list" && len(args) == 1 {
		names := make([]string, 0, len(views.Views))
		for name := range views.Views {
			names = append(names, name)
		}
		sort.Strings(names)
		if jsonOutput {
			return jsonOut(writeJSON(names))
		}
		for _, name := range names {
			_, _ = fmt.Fprintf(osStdout, "%s  source=%s  since=%s\n", name, views.Views[name].Source, views.Views[name].Since)
		}
		if len(names) == 0 {
			_, _ = fmt.Fprintln(osStdout, "No saved log views. Save filters with logs views save NAME or logs --interactive.")
		}
		return 0
	}
	if len(args) != 2 || args[0] != "show" && args[0] != "delete" {
		return printErr("Invalid log views usage", errors.New("use logs views list, show NAME, save NAME [filters], or delete NAME"))
	}
	view, exists := views.Views[args[1]]
	if !exists {
		return printErr("Unknown log view", errors.New("run gregale logs views list"))
	}
	if args[0] == "show" {
		if jsonOutput {
			return jsonOut(writeJSON(view))
		}
		_, _ = fmt.Fprintf(osStdout, "View %s (relative windows are recalculated on each run):\n", args[1])
		parts := []string{"gregale", "logs", "--app", "APP_SLUG"}
		for _, arg := range view.args() {
			parts = append(parts, quoteLogCommandArg(arg))
		}
		_, _ = fmt.Fprintln(osStdout, strings.Join(parts, " "))
		return 0
	}
	delete(views.Views, args[1])
	if err := writeLogViews(views); err != nil {
		return printErr("Could not delete log view", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"deleted": args[1]}))
	}
	PrintOK(osStdout, "Deleted log view %s", args[1])
	return 0
}
