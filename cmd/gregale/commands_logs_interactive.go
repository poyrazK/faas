package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func cmdLogsInteractive(slug string) int {
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose logs for "+slug+".", []string{"Runtime output from your app", "HTTP request events"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	source := logsSourceRuntime
	if choice == 1 {
		source = logsSourceHTTP
	}
	windows := []string{"15m", "1h", "24h"}
	window, err := prompt.choose(ctx, "Choose a time window.", []string{"Last 15 minutes", "Last hour", "Last 24 hours", "Enter a duration or timestamp"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	since := ""
	if window < len(windows) {
		since = windows[window]
	} else {
		for {
			since, err = prompt.text(ctx, "Lookback (for example 30m or 3d) or RFC3339 timestamp", "15m")
			if err != nil {
				return startInputExit(err)
			}
			if _, err := normalizeLogsSince(since, source, time.Now()); err == nil {
				break
			}
			_, _ = fmt.Fprintln(prompt.writer, "Use a positive duration or valid timestamp; HTTP timestamps must be in the past.")
		}
	}
	args := []string{slug, "--source", source, "--since", since}
	if source == logsSourceRuntime {
		level, err := prompt.choose(ctx, "Choose a log level.", []string{"All levels", "error", "warn", "info"}, 0)
		if err != nil {
			return startInputExit(err)
		}
		if level > 0 {
			args = append(args, "--level", []string{"", "error", "warn", "info"}[level])
		}
		grep, err := logsPromptFilter(ctx, prompt, "Text to match (optional)", false)
		if err != nil {
			return startInputExit(err)
		}
		if grep != "" {
			args = append(args, "--grep", grep)
		}
		follow, err := prompt.confirm(ctx, "Keep following new runtime lines until Ctrl-C?")
		if err != nil {
			return startInputExit(err)
		}
		if follow {
			args = append(args, "--follow")
		}
	} else {
		status, err := prompt.choose(ctx, "Choose an HTTP status filter (exact code).", []string{"All statuses", "500", "503", "404", "Enter another status"}, 0)
		if err != nil {
			return startInputExit(err)
		}
		if status > 0 {
			code := []int{0, 500, 503, 404, 0}[status]
			if status == 4 {
				code, err = scalePromptInt(ctx, prompt, "HTTP status", 500, 100, 599)
				if err != nil {
					return startInputExit(err)
				}
			}
			args = append(args, "--status", strconv.Itoa(code))
		}
		route, err := logsPromptFilter(ctx, prompt, "Route path (optional, for example /api/orders)", true)
		if err != nil {
			return startInputExit(err)
		}
		if route != "" {
			args = append(args, "--route", route)
		}
		_, _ = fmt.Fprintln(prompt.writer, "This query returns up to 100 HTTP request events.")
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "logs")
	for _, arg := range args {
		command = append(command, quoteLogCommandArg(arg))
	}
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	if view, _, viewErr := parseLogViewFlags(args[1:]); viewErr == nil {
		for {
			name, err := prompt.text(ctx, "Save as view (optional name; empty skips)", "")
			if err != nil {
				return startInputExit(err)
			}
			if name == "" {
				break
			}
			if err := validateProfileName(name); err != nil {
				_, _ = fmt.Fprintln(prompt.writer, err)
				continue
			}
			if err := saveLogView(name, view, false); err != nil {
				return printErr("Could not save log view", err)
			}
			PrintOK(osStdout, "Saved log view %s", name)
			break
		}
	} else {
		_, _ = fmt.Fprintln(prompt.writer, "Saving a view requires a relative time window; this timestamp query can still run.")
	}
	run, err := prompt.confirm(ctx, "Run this log query now?")
	if err != nil {
		return startInputExit(err)
	}
	if !run {
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	// Let the log handler own interruption while executing the query.
	stop()
	// Pass arguments directly to the existing parser, never through a shell.
	return cmdLogs(args)
}

func logsPromptFilter(ctx context.Context, prompt *startPrompt, label string, route bool) (string, error) {
	for {
		value, err := prompt.text(ctx, label, "")
		if err != nil {
			return "", err
		}
		if len(value) <= 2048 && strings.IndexFunc(value, unicode.IsControl) < 0 && (!route || value == "" || strings.HasPrefix(value, "/")) {
			return value, nil
		}
		_, _ = fmt.Fprintln(prompt.writer, "Use at most 2048 bytes without control characters; route paths must start with /.")
	}
}

func quoteLogCommandArg(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_/.:", r)
		return !safe
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
