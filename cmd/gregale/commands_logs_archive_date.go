package main

import (
	"errors"
	"time"
)

// Archives are keyed by UTC day, regardless of the customer's local timezone.
func resolveArchiveLogDate(raw string, now time.Time) (string, error) {
	switch raw {
	case "today":
		return now.UTC().Format("2006-01-02"), nil
	case "yesterday":
		return now.UTC().AddDate(0, 0, -1).Format("2006-01-02"), nil
	default:
		date, err := time.Parse("2006-01-02", raw)
		if err != nil || date.Format("2006-01-02") != raw {
			return "", errors.New("--date must be today, yesterday (UTC), or YYYY-MM-DD (for example, 2026-10-12)")
		}
		return raw, nil
	}
}
