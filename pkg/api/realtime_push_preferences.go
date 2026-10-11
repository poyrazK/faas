package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata"
)

type RealtimeQuietHours struct {
	Timezone string `json:"timezone"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

// Devices=null selects all registered devices; [] selects none. Missing
// categories inherit enabled=true. PUT replaces the entire preference document.
type RealtimeNotificationRateLimit struct {
	MaxNotifications  int  `json:"max_notifications"`
	WindowSeconds     int  `json:"window_seconds"`
	AllowUrgentBypass bool `json:"allow_urgent_bypass,omitempty"`
}

type RealtimeNotificationPreferences struct {
	RateLimit             *RealtimeNotificationRateLimit `json:"rate_limit,omitempty"`
	AllowUrgentBypass     bool                           `json:"allow_urgent_bypass,omitempty"`
	DigestIntervalSeconds int                            `json:"digest_interval_seconds,omitempty"`
	SummarizeQuietHours   *bool                          `json:"summarize_quiet_hours,omitempty"`
	Enabled               bool                           `json:"enabled"`
	Categories            map[string]bool                `json:"categories"`
	Devices               []string                       `json:"devices"`
	QuietHours            *RealtimeQuietHours            `json:"quiet_hours"`
}

func DefaultRealtimeNotificationPreferences() RealtimeNotificationPreferences {
	return RealtimeNotificationPreferences{Enabled: true, Categories: map[string]bool{}}
}
func ValidateRealtimeNotificationCategory(category string) error {
	if len(category) < 1 || len(category) > 64 {
		return errors.New("invalid notification category")
	}
	for _, r := range category {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return errors.New("invalid notification category")
		}
	}
	return nil
}
func quietMinute(value string) (int, error) {
	t, err := time.Parse("15:04", value)
	if err != nil || t.Format("15:04") != value {
		return 0, errors.New("quiet hours require HH:MM")
	}
	return t.Hour()*60 + t.Minute(), nil
}
func (p RealtimeNotificationPreferences) Validate() error {
	if r := p.RateLimit; r != nil && (r.MaxNotifications < 1 || r.MaxNotifications > 100 || (r.WindowSeconds != 60 && r.WindowSeconds != 300 && r.WindowSeconds != 3600)) {
		return errors.New("rate limit requires max_notifications 1..100 and window_seconds 60, 300 or 3600")
	}
	if p.DigestIntervalSeconds != 0 && p.DigestIntervalSeconds != 300 && p.DigestIntervalSeconds != 3600 {
		return errors.New("digest interval must be 0, 300 or 3600 seconds")
	}
	if len(p.Categories) > 32 || len(p.Devices) > 16 {
		return errors.New("notification preference limit exceeded")
	}
	for c := range p.Categories {
		if ValidateRealtimeNotificationCategory(c) != nil {
			return errors.New("invalid notification category")
		}
	}
	seen := map[string]bool{}
	for _, d := range p.Devices {
		if d == "" || len(d) > 128 || strings.TrimSpace(d) != d || strings.ContainsAny(d, "\x00\r\n") || seen[d] {
			return errors.New("invalid notification device")
		}
		seen[d] = true
	}
	if q := p.QuietHours; q != nil {
		if q.Timezone == "" || q.Timezone == "Local" || len(q.Timezone) > 128 {
			return errors.New("invalid quiet hours timezone")
		}
		if _, err := time.LoadLocation(q.Timezone); err != nil {
			return errors.New("invalid quiet hours timezone")
		}
		start, err := quietMinute(q.Start)
		if err != nil {
			return err
		}
		end, err := quietMinute(q.End)
		if err != nil {
			return err
		}
		if start == end {
			return errors.New("quiet hours start and end must differ")
		}
	}
	return nil
}

// NextPushTime evaluates real instants in the named timezone, including DST
// gaps and repeated hours. Quiet intervals include start and exclude end.
func (p RealtimeNotificationPreferences) NextPushTime(now time.Time, category, device string) (time.Time, bool) {
	if !p.Enabled {
		return time.Time{}, false
	}
	if enabled, exists := p.Categories[category]; exists && !enabled {
		return time.Time{}, false
	}
	if p.Devices != nil {
		found := false
		for _, d := range p.Devices {
			if d == device {
				found = true
				break
			}
		}
		if !found {
			return time.Time{}, false
		}
	}
	if p.QuietHours == nil {
		return now, true
	}
	q := p.QuietHours
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return time.Time{}, false
	}
	start, err := quietMinute(q.Start)
	if err != nil {
		return time.Time{}, false
	}
	end, err := quietMinute(q.End)
	if err != nil || start == end {
		return time.Time{}, false
	}
	quiet := func(at time.Time) bool {
		local := at.In(loc)
		minute := local.Hour()*60 + local.Minute()
		if start < end {
			return minute >= start && minute < end
		}
		return minute >= start || minute < end
	}
	if !quiet(now) {
		return now, true
	}
	next := now.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 48*60; i++ {
		if !quiet(next) {
			return next, true
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, false
}
func (c *Client) GetManagedRealtimeNotificationPreferences(ctx context.Context, slug, ep, principal string) (RealtimeNotificationPreferences, error) {
	var p RealtimeNotificationPreferences
	err := c.do(ctx, "GET", pushAPIPath(slug, ep)+"/preferences?principal="+url.QueryEscape(principal), nil, &p)
	return p, err
}
func (c *Client) PutManagedRealtimeNotificationPreferences(ctx context.Context, slug, ep, principal string, p RealtimeNotificationPreferences) (RealtimeNotificationPreferences, error) {
	var out RealtimeNotificationPreferences
	err := c.do(ctx, "PUT", pushAPIPath(slug, ep)+"/preferences?principal="+url.QueryEscape(principal), p, &out)
	return out, err
}

func DecodeRealtimeNotificationPreferences(raw []byte) (RealtimeNotificationPreferences, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return RealtimeNotificationPreferences{}, errors.New("invalid notification preferences")
	}
	if interval, exists := fields["digest_interval_seconds"]; exists && strings.TrimSpace(string(interval)) == "null" {
		return RealtimeNotificationPreferences{}, errors.New("invalid digest interval")
	}
	if urgent, exists := fields["allow_urgent_bypass"]; exists && strings.TrimSpace(string(urgent)) == "null" {
		return RealtimeNotificationPreferences{}, errors.New("allow_urgent_bypass must be boolean")
	}
	value, exists := fields["enabled"]
	if !exists || (strings.TrimSpace(string(value)) != "true" && strings.TrimSpace(string(value)) != "false") {
		return RealtimeNotificationPreferences{}, errors.New("enabled is required")
	}
	if limit, exists := fields["rate_limit"]; exists && strings.TrimSpace(string(limit)) != "null" {
		var rateFields map[string]json.RawMessage
		if json.Unmarshal(limit, &rateFields) != nil || rateFields == nil {
			return RealtimeNotificationPreferences{}, errors.New("invalid rate limit")
		}
		if bypass, exists := rateFields["allow_urgent_bypass"]; exists && strings.TrimSpace(string(bypass)) == "null" {
			return RealtimeNotificationPreferences{}, errors.New("rate limit urgent bypass must be boolean")
		}
	}
	var p RealtimeNotificationPreferences
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

func ValidateRealtimeNotificationGroup(key, label string) error {
	if len(key) > 128 || len(label) > 128 || strings.TrimSpace(key) != key || strings.TrimSpace(label) != label || strings.ContainsAny(key+label, "\x00\r\n") {
		return errors.New("invalid notification group")
	}
	if label != "" && key == "" {
		return errors.New("group label requires group key")
	}
	return nil
}

func ValidateRealtimeNotificationPriority(priority string) error {
	switch priority {
	case "", "low", "normal", "urgent":
		return nil
	default:
		return errors.New("notification priority must be low, normal or urgent")
	}
}

// Past instants are valid and become immediately eligible after fallback.
func ParseRealtimeNotificationNotBefore(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || at.After(now.Add(48*time.Hour)) {
		return time.Time{}, errors.New("notification_not_before must be RFC3339 with timezone and at most 48 hours ahead")
	}
	return at.UTC(), nil
}

func ValidateRealtimeMetadata(values map[string]string) error {
	raw, _ := json.Marshal(values)
	if len(raw) > 4096 {
		return errors.New("encoded metadata exceeds 4096 bytes")
	}
	if len(values) > 8 {
		return errors.New("metadata allows at most 8 entries")
	}
	for key, value := range values {
		if len(key) < 1 || len(key) > 64 || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("invalid metadata")
		}
		for _, r := range key {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
				return errors.New("invalid metadata key")
			}
		}
	}
	return nil
}
