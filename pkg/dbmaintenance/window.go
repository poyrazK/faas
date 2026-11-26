// Package dbmaintenance defines the weekly UTC window used by native PostgreSQL.
package dbmaintenance

import "time"

type Window struct {
	Weekday         int `json:"weekday"`
	StartMinute     int `json:"start_minute"`
	DurationMinutes int `json:"duration_minutes"`
}

func (w Window) Valid() bool {
	return w.Weekday >= 0 && w.Weekday <= 6 && w.StartMinute >= 0 &&
		w.StartMinute < 24*60 && w.DurationMinutes > 0 && w.DurationMinutes <= 24*60
}

type Interval struct {
	Start, End time.Time
}

func (i Interval) Contains(at time.Time) bool {
	return !at.Before(i.Start) && at.Before(i.End)
}

// ActiveOrNext returns the current interval, or the next opening. The end
// is exclusive. The most recent opening may have been on the previous day.
func (w Window) ActiveOrNext(now time.Time) Interval {
	now = now.UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	daysAgo := (int(now.Weekday()) - w.Weekday + 7) % 7
	start := midnight.AddDate(0, 0, -daysAgo).Add(time.Duration(w.StartMinute) * time.Minute)
	if now.Before(start) {
		start = start.AddDate(0, 0, -7)
	}
	duration := time.Duration(w.DurationMinutes) * time.Minute
	end := start.Add(duration)
	if !now.Before(end) {
		start = start.AddDate(0, 0, 7)
		end = start.Add(duration)
	}
	return Interval{Start: start, End: end}
}
