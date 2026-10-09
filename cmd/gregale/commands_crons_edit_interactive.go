package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
)

func cmdCronsUpdateInteractive(slug string) int {
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(readCtx, slug)
	if err != nil {
		cancel()
		return printErr("Could not read app", err)
	}
	crons, err := client.ListCrons(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not list scheduled tasks", err)
	}
	var candidates []api.CronResponse
	for _, task := range crons {
		if task.AppID != app.ID || !cronIDPattern.MatchString(task.ID) {
			return printErr("Invalid task list", errors.New("a task does not match the selected app or has an invalid ID"))
		}
		if cronKindOrHTTP(task.Kind) == "http" && len(task.Command) == 0 {
			candidates = append(candidates, task)
		}
	}
	if len(candidates) == 0 {
		PrintProgress(osStdout, "No scheduled HTTP tasks available for %s.", slug)
		return 0
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	labels := make([]string, 0, len(candidates)+1)
	for _, task := range candidates {
		labels = append(labels, fmt.Sprintf("%s · %s · %s · enabled=%t", task.ID, task.Path, task.Schedule, task.Enabled))
	}
	labels = append(labels, "Cancel")
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose an HTTP task to edit for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(candidates) {
		PrintProgress(osStdout, "Task editing canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	original, err := client.GetCron(readCtx, candidates[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected task", err)
	}
	if !sameBindingDeployment(original.ID, candidates[choice].ID) || original.AppID != app.ID || cronKindOrHTTP(original.Kind) != "http" || len(original.Command) != 0 {
		return printErr("Selected task changed", errors.New("run the guide again to select an HTTP task for this app"))
	}
	PrintProgress(osStdout, "Current task:")
	renderCronInfo(osStdout, original)
	path := original.Path
	if path == "" {
		path = "/"
	}
	for {
		path, err = prompt.text(ctx, "HTTP request path", path)
		if err != nil {
			return startInputExit(err)
		}
		u, parseErr := url.ParseRequestURI(path)
		if parseErr == nil && len(path) <= 2048 && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && strings.IndexFunc(path, unicode.IsControl) < 0 && u.Host == "" && u.Scheme == "" && !strings.Contains(path, "#") {
			break
		}
		_, _ = fmt.Fprintln(prompt.writer, "Use an app-relative path beginning with /, without a host, fragment, or control characters (at most 2048 bytes).")
		path = original.Path
		if path == "" {
			path = "/"
		}
	}
	timezone, err := cronexpr.NormalizeTimezone(original.Timezone)
	if err != nil {
		return printErr("Invalid current timezone", err)
	}
	for {
		value, err := prompt.text(ctx, "Timezone (explicit IANA name)", timezone)
		if err != nil {
			return startInputExit(err)
		}
		if len(value) <= 128 && value != "Local" && strings.IndexFunc(value, unicode.IsControl) < 0 {
			if normalized, normalizeErr := cronexpr.NormalizeTimezone(value); normalizeErr == nil {
				timezone = normalized
				break
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Use UTC or an explicit IANA timezone such as Europe/Istanbul.")
	}
	expression := original.Schedule
	for {
		expression, err = prompt.text(ctx, "Schedule (minute hour day-of-month month day-of-week)", original.Schedule)
		if err != nil {
			return startInputExit(err)
		}
		if len(expression) <= 256 && strings.IndexFunc(expression, unicode.IsControl) < 0 {
			if _, parseErr := cronexpr.Parse(expression, timezone); parseErr == nil {
				break
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Enter a valid five-field expression with future run times.")
	}
	fallback := 1
	if original.Enabled {
		fallback = 0
	}
	state, err := prompt.choose(ctx, "Task state", []string{"Enabled", "Disabled"}, fallback)
	if err != nil {
		return startInputExit(err)
	}
	enabled := state == 0
	req := api.UpdateCronRequest{}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "crons", "update", quoteLogCommandArg(original.ID))
	PrintProgress(osStdout, "Proposed changes for app=%s; task=%s:", slug, original.ID)
	originalPath := original.Path
	if originalPath == "" {
		originalPath = "/"
	}
	if path != originalPath {
		req.Path = &path
		PrintProgress(osStdout, "Path: %s -> %s", originalPath, path)
		command = append(command, "--path", quoteLogCommandArg(path))
	}
	if expression != original.Schedule {
		req.Schedule = &expression
		PrintProgress(osStdout, "Schedule: %s -> %s", original.Schedule, expression)
		command = append(command, "--schedule", quoteLogCommandArg(expression))
	}
	oldTimezone, _ := cronexpr.NormalizeTimezone(original.Timezone)
	if timezone != oldTimezone {
		req.Timezone = &timezone
		PrintProgress(osStdout, "Timezone: %s -> %s", oldTimezone, timezone)
		command = append(command, "--timezone", quoteLogCommandArg(timezone))
	}
	if enabled != original.Enabled {
		req.Enabled = &enabled
		PrintProgress(osStdout, "Enabled: %t -> %t", original.Enabled, enabled)
		flag := "--disable"
		if enabled {
			flag = "--enable"
		}
		command = append(command, flag)
	}
	if req.Path == nil && req.Schedule == nil && req.Timezone == nil && req.Enabled == nil {
		PrintProgress(osStdout, "No changes selected.")
		return 0
	}
	schedule, err := cronexpr.Parse(expression, timezone)
	if err != nil {
		return printErr("Invalid proposed schedule", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return printErr("Invalid proposed timezone", err)
	}
	_, _ = fmt.Fprintln(osStdout, "Next expression times (policy skips and execution delays may apply):")
	next := time.Now()
	for i := 0; i < 3; i++ {
		next = schedule.Next(next)
		if next.IsZero() {
			return printErr("No future run time", errors.New("choose another schedule"))
		}
		_, _ = fmt.Fprintln(osStdout, "  "+next.In(location).Format(time.RFC3339))
	}
	if !enabled {
		PrintProgress(osStdout, "Task will be disabled; these times are illustrative and will not fire.")
	}
	if original.SuspendedReason != "" {
		PrintProgress(osStdout, "Task remains suspended: %s", original.SuspendedReason)
	}
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	confirmed, err := prompt.confirm(ctx, "Save these task changes?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Task was not changed.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.GetCron(readCtx, original.ID)
	cancel()
	if err != nil {
		return printErr("Could not recheck task", err)
	}
	// Fires update LastFiredAt independently of configuration edits.
	original.LastFiredAt, latest.LastFiredAt = "", ""
	if !reflect.DeepEqual(original, latest) {
		return printErr("Task changed during review", errors.New("run the guide again to review its current configuration"))
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	return updateCronAndRender(ctx, client, original.ID, req)
}
