package objectstorage

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// LifecycleObject describes one version in a complete exact-key history. The
// worker must establish this history under a mutation fence before dispatch.
type LifecycleObject struct {
	Key                    string
	LastModified           time.Time
	IsLatest, DeleteMarker bool
	NoncurrentSince        time.Time
	NewerNoncurrent        int
	OnlyVersion            bool
	Tags                   map[string]string
}

type LifecycleDecision struct {
	Kind, RuleID string
}

// SelectLifecycleAction chooses one eligible expiration. Tags are evaluated
// by presence AND exact value; an absent tag never matches an empty value.
func SelectLifecycleAction(rules []api.ObjectLifecycleRule, o LifecycleObject, now time.Time) (LifecycleDecision, error) {
	valid, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil || !ValidKey(o.Key) || o.LastModified.IsZero() || o.LastModified.After(now) || o.NewerNoncurrent < 0 || o.OnlyVersion && !o.IsLatest || o.IsLatest && (o.NewerNoncurrent != 0 || !o.NoncurrentSince.IsZero()) || !o.IsLatest && (o.NoncurrentSince.IsZero() || o.NoncurrentSince.Before(o.LastModified) || o.NoncurrentSince.After(now)) {
		return LifecycleDecision{}, ErrInvalid
	}
	for _, r := range valid {
		if r.Status != "Enabled" || !lifecycleFilterMatches(r.Filter, o.Key, o.Tags) {
			continue
		}
		if o.IsLatest && o.DeleteMarker && o.OnlyVersion && r.Expiration != nil && expirationMarkerEligible(*r.Expiration, o.LastModified, now) {
			return LifecycleDecision{Kind: "expired_marker", RuleID: r.ID}, nil
		}
		if o.IsLatest && !o.DeleteMarker && r.Expiration != nil && lifecycleExpirationDue(*r.Expiration, o.LastModified, now) {
			return LifecycleDecision{Kind: "current", RuleID: r.ID}, nil
		}
		if !o.IsLatest && r.NoncurrentVersionExpiration != nil {
			v := r.NoncurrentVersionExpiration
			if v.NewerNoncurrentVersions != nil && o.NewerNoncurrent < int(*v.NewerNoncurrentVersions) {
				continue
			}
			if !now.Before(lifecycleDayDeadline(o.NoncurrentSince, v.NoncurrentDays)) {
				return LifecycleDecision{Kind: "noncurrent", RuleID: r.ID}, nil
			}
		}
	}
	return LifecycleDecision{}, nil
}

func lifecycleFilterMatches(filter api.ObjectLifecycleFilter, key string, tags map[string]string) bool {
	if !strings.HasPrefix(key, filter.Prefix) {
		return false
	}
	for name, value := range filter.Tags {
		actual, present := tags[name]
		if !present || actual != value {
			return false
		}
	}
	return true
}

func lifecycleExpirationDue(e api.ObjectLifecycleExpiration, modified, now time.Time) bool {
	if e.Date != nil {
		return !now.Before(*e.Date)
	}
	return e.Days != nil && !now.Before(lifecycleDayDeadline(modified, *e.Days))
}

func expirationMarkerEligible(e api.ObjectLifecycleExpiration, modified, now time.Time) bool {
	if e.ExpiredObjectDeleteMarker != nil {
		return *e.ExpiredObjectDeleteMarker
	}
	return lifecycleExpirationDue(e, modified, now)
}

func lifecycleDayDeadline(base time.Time, days int32) time.Time {
	v := base.UTC().AddDate(0, 0, int(days))
	// S3 evaluates age-based actions at the next day's midnight UTC. In
	// particular, an exact-midnight timestamp must not dispatch a day early.
	return time.Date(v.Year(), v.Month(), v.Day()+1, 0, 0, 0, 0, time.UTC)
}

func LifecycleMultipartAbortDue(rules []api.ObjectLifecycleRule, key string, initiated, now time.Time) (LifecycleDecision, error) {
	valid, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil || !ValidKey(key) || initiated.IsZero() || initiated.After(now) {
		return LifecycleDecision{}, ErrInvalid
	}
	for _, r := range valid {
		if r.Status == "Enabled" && r.AbortIncompleteMultipartDays != nil && lifecycleFilterMatches(r.Filter, key, nil) && !now.Before(lifecycleDayDeadline(initiated, *r.AbortIncompleteMultipartDays)) {
			return LifecycleDecision{Kind: "abort_multipart", RuleID: r.ID}, nil
		}
	}
	return LifecycleDecision{}, nil
}
