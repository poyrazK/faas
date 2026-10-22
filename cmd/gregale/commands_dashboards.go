package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const dashboardsUsage = "usage: gregale dashboards <list|show|get|create|replace|rm>"

// cmdDashboards implements `gregale dashboards` (ADR-20261022173833): an
// account's saved custom dashboards.
func cmdDashboards(args []string) int {
	if len(args) == 0 {
		args = []string{subList}
	}
	switch args[0] {
	case subList:
		return cmdDashboardsList(args[1:])
	case "get":
		return cmdDashboardsGet(args[1:])
	case "show":
		return cmdDashboardsShow(args[1:])
	case "create":
		return cmdDashboardsCreate(args[1:])
	case "replace":
		return cmdDashboardsReplace(args[1:])
	case subRm:
		return cmdDashboardsRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown dashboards subcommand %q\n", args[0])
	PrintUsage(os.Stderr, dashboardsUsage, "dashboards")
	return 1
}

func cmdDashboardsList(args []string) int {
	fs := newFlagSet("dashboards list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale dashboards list", "dashboards")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListDashboards(context.Background())
	if err != nil {
		return printErr("Could not list dashboards", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Dashboards) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No dashboards. Create one with `gregale dashboards create NAME --file widgets.json`.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s  %-24s  %s\n", "ID", "NAME", "WIDGETS")
	for _, d := range out.Dashboards {
		_, _ = fmt.Fprintf(osStdout, "%-36s  %-24s  %d\n", d.ID, d.Name, len(d.Widgets))
	}
	return 0
}

func cmdDashboardsGet(args []string) int {
	fs := newFlagSet("dashboards get", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale dashboards get <dashboard-id>", "dashboards")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.GetDashboard(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not read the dashboard", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	renderDashboard(osStdout, d)
	return 0
}

// cmdDashboardsCreate implements `gregale dashboards create <name> --file F`;
// F holds the widgets as a JSON array (or a {"widgets": [...]} object).
func cmdDashboardsCreate(args []string) int {
	const usage = "usage: gregale dashboards create <name> --file <widgets.json>"
	fs := newFlagSet("dashboards create", flag.ContinueOnError)
	file := fs.String("file", "", "JSON file with the widgets array (- for stdin)")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if len(pos) != 1 || *file == "" {
		PrintUsage(os.Stderr, usage, "dashboards")
		return 1
	}
	widgets, err := readDashboardWidgets(*file)
	if err != nil {
		return printErr("Invalid widgets file", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.CreateDashboard(context.Background(), api.DashboardRequest{Name: pos[0], Widgets: widgets})
	if err != nil {
		return printErr("Could not create the dashboard", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	_, _ = fmt.Fprintf(osStdout, "created dashboard %s (%s) with %d widgets\n", d.Name, d.ID, len(d.Widgets))
	return 0
}

// cmdDashboardsReplace implements `gregale dashboards replace <id> --name N --file F`.
func cmdDashboardsReplace(args []string) int {
	const usage = "usage: gregale dashboards replace <dashboard-id> --name <name> --file <widgets.json>"
	fs := newFlagSet("dashboards replace", flag.ContinueOnError)
	name := fs.String("name", "", "dashboard name")
	file := fs.String("file", "", "JSON file with the widgets array (- for stdin)")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if len(pos) != 1 || *name == "" || *file == "" {
		PrintUsage(os.Stderr, usage, "dashboards")
		return 1
	}
	widgets, err := readDashboardWidgets(*file)
	if err != nil {
		return printErr("Invalid widgets file", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.ReplaceDashboard(context.Background(), pos[0], api.DashboardRequest{Name: *name, Widgets: widgets})
	if err != nil {
		return printErr("Could not replace the dashboard", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	_, _ = fmt.Fprintf(osStdout, "replaced dashboard %s (%s) with %d widgets\n", d.Name, d.ID, len(d.Widgets))
	return 0
}

func cmdDashboardsRm(args []string) int {
	fs := newFlagSet("dashboards rm", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale dashboards rm <dashboard-id>", "dashboards")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteDashboard(context.Background(), fs.Arg(0)); err != nil {
		return printErr("Could not delete the dashboard", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"deleted": fs.Arg(0)}))
	}
	_, _ = fmt.Fprintf(osStdout, "deleted dashboard %s\n", fs.Arg(0))
	return 0
}

// cmdDashboardsShow implements `gregale dashboards show <id>`: every widget's
// current value.
func cmdDashboardsShow(args []string) int {
	fs := newFlagSet("dashboards show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale dashboards show <dashboard-id>", "dashboards")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	data, err := client.GetDashboardData(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not read the dashboard", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(data))
	}
	renderDashboardData(osStdout, data)
	return 0
}

func renderDashboardData(w io.Writer, data api.DashboardData) {
	_, _ = fmt.Fprintf(w, "%s as of %s\n", data.Name, data.AsOf.Format("2006-01-02 15:04:05 UTC"))
	for _, widget := range data.Widgets {
		value := "-"
		switch {
		case widget.Error != "":
			value = "unavailable: " + widget.Error
		case widget.Value != nil:
			value = strings.TrimSpace(fmt.Sprintf("%s %s", strconv.FormatFloat(*widget.Value, 'f', -1, 64), widget.Unit))
		}
		window := widget.Window
		if window == "" {
			window = "-"
		}
		line := fmt.Sprintf("  %-28s %-6s %s", widget.Title, window, value)
		if widget.Detail != "" {
			line += "  (" + widget.Detail + ")"
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// readDashboardWidgets reads a widgets array, or an object holding one.
func readDashboardWidgets(path string) ([]api.DashboardWidget, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var widgets []api.DashboardWidget
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		var req api.DashboardRequest
		err = json.Unmarshal(raw, &req)
		widgets = req.Widgets
	} else {
		err = json.Unmarshal(raw, &widgets)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return widgets, nil
}

func renderDashboard(w io.Writer, d api.Dashboard) {
	_, _ = fmt.Fprintf(w, "%s (%s)\n", d.Name, d.ID)
	for i, widget := range d.Widgets {
		target := widget.Metric
		switch widget.Kind {
		case api.DashboardWidgetLogCount:
			target = widget.Query
		case api.DashboardWidgetSLO:
			target = widget.SLOID
		}
		window := ""
		if widget.Window != "" {
			window = " over " + widget.Window
		}
		_, _ = fmt.Fprintf(w, "  %2d. %-28s %s %s/%s%s\n", i+1, widget.Title, widget.Kind, widget.App, target, window)
	}
}
