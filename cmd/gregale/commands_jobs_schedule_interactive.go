package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/cronexpr"
)

func promptJobSchedule(ctx context.Context, prompt *startPrompt) (string, string, error) {
	timezone := "UTC"
	for {
		value, err := prompt.text(ctx, "Timezone (UTC or IANA name, e.g. Europe/Istanbul)", timezone)
		if err != nil {
			return "", "", err
		}
		if len(value) <= 128 && value != "Local" && strings.IndexFunc(value, unicode.IsControl) < 0 {
			normalized, err := cronexpr.NormalizeTimezone(value)
			if err == nil {
				timezone = normalized
				break
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Choose UTC or an explicit IANA timezone.")
	}
	presets := []string{"*/5 * * * *", "0 * * * *", "0 9 * * *", "0 9 * * 1-5"}
	choice, err := prompt.choose(ctx, "Choose a schedule (times use "+timezone+").", []string{"Every 5 minutes", "Hourly at minute 0", "Daily at 09:00", "Weekdays at 09:00", "Custom five-field expression"}, 1)
	if err != nil {
		return "", "", err
	}
	expression := "0 * * * *"
	if choice < len(presets) {
		expression = presets[choice]
	}
	for {
		if choice == len(presets) {
			expression, err = prompt.text(ctx, "Cron expression (minute hour day-of-month month day-of-week)", expression)
			if err != nil {
				return "", "", err
			}
		}
		if len(expression) <= 256 && strings.IndexFunc(expression, unicode.IsControl) < 0 {
			schedule, parseErr := cronexpr.Parse(expression, timezone)
			if parseErr == nil && !schedule.Next(time.Now()).IsZero() {
				return expression, timezone, nil
			}
		}
		_, _ = fmt.Fprintln(prompt.writer, "Enter a valid five-field expression with future scheduled times.")
		choice = len(presets)
	}
}

func previewJobSchedule(expression, timezone string) error {
	schedule, err := cronexpr.Parse(expression, timezone)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	PrintProgress(osStdout, "Schedule: %s; timezone: %s\nNext nominal scheduled times:", expression, timezone)
	next := time.Now()
	for i := 0; i < 3; i++ {
		next = schedule.Next(next)
		if next.IsZero() {
			return errors.New("schedule has no next time")
		}
		_, _ = fmt.Fprintln(osStdout, "  "+next.In(location).Format(time.RFC3339))
	}
	return nil
}
