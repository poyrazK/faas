package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidObjectLifecycle = errors.New("invalid object lifecycle configuration")

type ObjectBucketLifecycleRequest struct {
	Rules []ObjectLifecycleRule `json:"rules"`
}

type ObjectLifecycleFilter struct {
	Prefix string            `json:"prefix,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

type ObjectLifecycleExpiration struct {
	Days                      *int32     `json:"days,omitempty"`
	Date                      *time.Time `json:"date,omitempty"`
	ExpiredObjectDeleteMarker *bool      `json:"expired_object_delete_marker,omitempty"`
}

type ObjectLifecycleNoncurrentExpiration struct {
	NoncurrentDays          int32  `json:"noncurrent_days"`
	NewerNoncurrentVersions *int32 `json:"newer_noncurrent_versions,omitempty"`
}

type ObjectLifecycleRule struct {
	ID                           string                               `json:"id"`
	Status                       string                               `json:"status"`
	Filter                       ObjectLifecycleFilter                `json:"filter"`
	Expiration                   *ObjectLifecycleExpiration           `json:"expiration,omitempty"`
	NoncurrentVersionExpiration  *ObjectLifecycleNoncurrentExpiration `json:"noncurrent_version_expiration,omitempty"`
	AbortIncompleteMultipartDays *int32                               `json:"abort_incomplete_multipart_days,omitempty"`
}

type ObjectBucketLifecycle struct {
	BucketID  string                `json:"bucket_id"`
	Revision  int64                 `json:"revision"`
	Rules     []ObjectLifecycleRule `json:"rules"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type ObjectLifecycleScan struct {
	ID             string     `json:"id"`
	BucketID       string     `json:"bucket_id"`
	Revision       int64      `json:"revision"`
	State          string     `json:"state"`
	Phase          string     `json:"phase"`
	ScannedKeys    int64      `json:"scanned_keys"`
	ScannedUploads int64      `json:"scanned_uploads"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// ObjectLifecycleMultipartAbortRule selects a normalized rule using the
// original initiation time. The conservative day boundary matches expiration
// discovery; callers must recheck it while admitting the durable abort.
func ObjectLifecycleMultipartAbortRule(rules []ObjectLifecycleRule, key string, initiated, now time.Time) (string, error) {
	valid, err := NormalizeObjectLifecycleRules(rules)
	if err != nil || key == "" || len(key) > MaxObjectS3ListTextBytes || !validLifecycleText(key) || initiated.IsZero() || initiated.After(now) {
		return "", ErrInvalidObjectLifecycle
	}
	for _, r := range valid {
		if r.Status != "Enabled" || r.AbortIncompleteMultipartDays == nil || !strings.HasPrefix(key, r.Filter.Prefix) {
			continue
		}
		v := initiated.UTC().AddDate(0, 0, int(*r.AbortIncompleteMultipartDays))
		deadline := time.Date(v.Year(), v.Month(), v.Day()+1, 0, 0, 0, 0, time.UTC)
		if !now.Before(deadline) {
			return r.ID, nil
		}
	}
	return "", nil
}

// NormalizeObjectLifecycleRules makes a detached, deterministic configuration.
// Empty rules represent removal; a public S3 PUT must require at least one rule.
func NormalizeObjectLifecycleRules(rules []ObjectLifecycleRule) ([]ObjectLifecycleRule, error) {
	if len(rules) > MaxObjectLifecycleRules {
		return nil, ErrInvalidObjectLifecycle
	}
	out := make([]ObjectLifecycleRule, 0, len(rules))
	seen := map[string]bool{}
	for i, rule := range rules {
		if !validObjectLifecycleRule(rule) {
			return nil, ErrInvalidObjectLifecycle
		}
		rule = cloneObjectLifecycleRule(rule)
		if rule.ID == "" {
			raw, err := json.Marshal(rule)
			if err != nil {
				return nil, ErrInvalidObjectLifecycle
			}
			hash := sha256.Sum256(append([]byte(strconv.Itoa(i)+":"), raw...))
			rule.ID = "rule-" + hex.EncodeToString(hash[:12])
		}
		if seen[rule.ID] {
			return nil, ErrInvalidObjectLifecycle
		}
		seen[rule.ID] = true
		out = append(out, rule)
	}
	raw, err := json.Marshal(out)
	if err != nil || int64(len(raw)) > MaxObjectLifecycleBodyBytes {
		return nil, ErrInvalidObjectLifecycle
	}
	return out, nil
}

func CloneObjectLifecycleRules(rules []ObjectLifecycleRule) []ObjectLifecycleRule {
	out := make([]ObjectLifecycleRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, cloneObjectLifecycleRule(rule))
	}
	return out
}

func cloneObjectLifecycleRule(r ObjectLifecycleRule) ObjectLifecycleRule {
	r.Filter.Tags = maps.Clone(r.Filter.Tags)
	if r.Expiration != nil {
		v := *r.Expiration
		v.Days, v.Date, v.ExpiredObjectDeleteMarker = cloneLifecyclePointer(v.Days), cloneLifecyclePointer(v.Date), cloneLifecyclePointer(v.ExpiredObjectDeleteMarker)
		if v.Date != nil {
			*v.Date = v.Date.UTC()
		}
		r.Expiration = &v
	}
	if r.NoncurrentVersionExpiration != nil {
		v := *r.NoncurrentVersionExpiration
		v.NewerNoncurrentVersions = cloneLifecyclePointer(v.NewerNoncurrentVersions)
		r.NoncurrentVersionExpiration = &v
	}
	r.AbortIncompleteMultipartDays = cloneLifecyclePointer(r.AbortIncompleteMultipartDays)
	return r
}

func cloneLifecyclePointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func validObjectLifecycleRule(r ObjectLifecycleRule) bool {
	if !validLifecycleText(r.ID) || utf8.RuneCountInString(r.ID) > MaxObjectLifecycleRuleIDRunes || r.Status != "Enabled" && r.Status != "Disabled" || len(r.Filter.Prefix) > MaxObjectS3ListTextBytes || !validLifecycleText(r.Filter.Prefix) || len(r.Filter.Tags) > MaxObjectTags {
		return false
	}
	for key, value := range r.Filter.Tags {
		if key == "" || len(key) > MaxObjectTagKeyBytes || len(value) > MaxObjectTagValueBytes || !validLifecycleText(key) || !validLifecycleText(value) {
			return false
		}
	}
	if r.Expiration == nil && r.NoncurrentVersionExpiration == nil && r.AbortIncompleteMultipartDays == nil {
		return false
	}
	if r.Expiration != nil && !validObjectLifecycleExpiration(*r.Expiration) {
		return false
	}
	if r.NoncurrentVersionExpiration != nil {
		v := r.NoncurrentVersionExpiration
		if v.NoncurrentDays < 1 || v.NewerNoncurrentVersions != nil && (*v.NewerNoncurrentVersions < 1 || *v.NewerNoncurrentVersions > MaxObjectLifecycleRetainedVersions) {
			return false
		}
	}
	return (r.AbortIncompleteMultipartDays == nil || *r.AbortIncompleteMultipartDays > 0) && (len(r.Filter.Tags) == 0 || r.AbortIncompleteMultipartDays == nil && (r.Expiration == nil || r.Expiration.ExpiredObjectDeleteMarker == nil))
}

func validObjectLifecycleExpiration(e ObjectLifecycleExpiration) bool {
	count := 0
	if e.Days != nil {
		count++
		if *e.Days < 1 {
			return false
		}
	}
	if e.Date != nil {
		count++
		v := e.Date.UTC()
		if v.IsZero() || v.Year() < 1 || v.Year() > 9999 || v.Hour() != 0 || v.Minute() != 0 || v.Second() != 0 || v.Nanosecond() != 0 {
			return false
		}
	}
	if e.ExpiredObjectDeleteMarker != nil {
		count++
	}
	return count == 1
}

func validLifecycleText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 || r == 0xfffe || r == 0xffff {
			return false
		}
	}
	return true
}
