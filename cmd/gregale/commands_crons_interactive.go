package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
)

func cmdCronsAddInteractive(slug string) int {
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
	_, err = client.GetApp(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not read app", err)
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	_, _ = fmt.Fprintln(prompt.writer, "This creates an enabled scheduled HTTP task for the app. Use --command flags for deployment command tasks.")
	var path string
	for {
		path, err = prompt.text(ctx, "HTTP request path", "/")
		if err != nil {
			return startInputExit(err)
		}
		u, parseErr := url.ParseRequestURI(path)
		if parseErr == nil && len(path) <= 2048 && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && strings.IndexFunc(path, unicode.IsControl) < 0 && u.Host == "" && u.Scheme == "" && !strings.Contains(path, "#") {
			break
		}
		_, _ = fmt.Fprintln(prompt.writer, "Enter an app-relative path starting with /, at most 2048 bytes, without a host, fragment, or control characters.")
	}
	var timezone string
	for {
		timezone, err = prompt.text(ctx, "Timezone (IANA name, for example Europe/Istanbul)", "UTC")
		if err != nil {
			return startInputExit(err)
		}
		if len(timezone) <= 128 && strings.IndexFunc(timezone, unicode.IsControl) < 0 && timezone != "Local" {
			if normalized, normalizeErr := cronexpr.NormalizeTimezone(timezone); normalizeErr == nil {
				timezone = normalized
				break
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Choose UTC or an explicit IANA timezone such as Europe/Istanbul.")
	}
	choice, err := prompt.choose(ctx, "Choose a schedule (times use "+timezone+").", []string{"Every 5 minutes", "Hourly at minute 0", "Daily at 09:00", "Weekdays at 09:00", "Enter a custom five-field expression"}, 1)
	if err != nil {
		return startInputExit(err)
	}
	expressions := []string{"*/5 * * * *", "0 * * * *", "0 9 * * *", "0 9 * * 1-5"}
	expression := ""
	if choice < len(expressions) {
		expression = expressions[choice]
	}
	for {
		if choice == len(expressions) {
			expression, err = prompt.text(ctx, "Cron expression (minute hour day-of-month month day-of-week)", "0 * * * *")
			if err != nil {
				return startInputExit(err)
			}
		}
		if len(expression) <= 256 && strings.IndexFunc(expression, unicode.IsControl) < 0 {
			if _, parseErr := cronexpr.Parse(expression, timezone); parseErr == nil {
				break
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Enter a valid five-field expression that has future run times.")
		choice = len(expressions)
	}
	// The shared grammar includes Gregale's daylight-saving behavior.
	schedule, err := cronexpr.Parse(expression, timezone)
	if err != nil {
		return printErr("Invalid schedule", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return printErr("Invalid timezone", err)
	}
	PrintProgress(osStdout, "Review scheduled HTTP task: app=%s; path=%s; schedule=%s; timezone=%s; enabled=true", slug, path, expression, timezone)
	_, _ = fmt.Fprintln(osStdout, "Next scheduled times (actual execution may be delayed):")
	next := time.Now()
	for i := 0; i < 3; i++ {
		next = schedule.Next(next)
		if next.IsZero() {
			return printErr("No future run time", errors.New("choose another schedule"))
		}
		_, _ = fmt.Fprintln(osStdout, "  "+next.In(location).Format(time.RFC3339))
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "crons", "add", "--app", quoteLogCommandArg(slug), "--schedule", quoteLogCommandArg(expression), "--path", quoteLogCommandArg(path), "--timezone", quoteLogCommandArg(timezone))
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	confirmed, err := prompt.confirm(ctx, "Create this scheduled HTTP task?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No scheduled task created.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	return createCronAndRender(ctx, client, slug, api.CreateCronRequest{AppID: slug, Schedule: expression, Path: path, Timezone: timezone, Enabled: boolPtr(true), SkipIfRunning: boolPtr(false)})
}
