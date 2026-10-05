package api

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ObjectNotificationRule routes proven mutations to a Gregale-owned destination.
// Prefix and suffix are decoded key strings in the control API.
type ObjectNotificationRule struct {
	ID          string   `json:"id"`
	Destination string   `json:"destination"`
	Events      []string `json:"events"`
	Prefix      string   `json:"prefix,omitempty"`
	Suffix      string   `json:"suffix,omitempty"`
}

type ObjectBucketNotifications struct {
	BucketID string                   `json:"bucket_id"`
	Revision int64                    `json:"revision"`
	Rules    []ObjectNotificationRule `json:"rules"`
}

type ObjectBucketNotificationsRequest struct {
	Rules []ObjectNotificationRule `json:"rules"`
}

type ObjectNotificationTarget struct{ Kind, Region, AccountID, AppID, QueueName string }

func ParseObjectNotificationTarget(arn string) (ObjectNotificationTarget, error) {
	p := strings.SplitN(arn, ":", 6)
	bad := errors.New("notification destination must be a Gregale function or queue ARN")
	if len(p) != 6 || p[0] != "arn" || p[1] != "gregale" || p[3] == "" {
		return ObjectNotificationTarget{}, bad
	}
	t := ObjectNotificationTarget{Region: p[3], AccountID: p[4]}
	switch p[2] {
	case "lambda":
		if !strings.HasPrefix(p[5], "function:") {
			return t, bad
		}
		t.Kind, t.AppID = "function", strings.TrimPrefix(p[5], "function:")
	case "sqs":
		app, queue, ok := strings.Cut(p[5], "/")
		if !ok || queue == "" || len(queue) > MaxObjectNotificationQueueNameBytes {
			return t, bad
		}
		if queue[0] < 'a' || queue[0] > 'z' {
			return t, bad
		}
		for _, c := range queue {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return t, bad
			}
		}
		t.Kind, t.AppID, t.QueueName = "queue", app, queue
	default:
		return t, bad
	}
	for _, s := range []string{t.AccountID, t.AppID} {
		id, e := uuid.Parse(s)
		if e != nil || id.String() != s {
			return t, bad
		}
	}
	return t, nil
}

var objectNotificationEvents = []string{"s3:ObjectCreated:Put", "s3:ObjectCreated:Copy", "s3:ObjectCreated:CompleteMultipartUpload", "s3:ObjectRemoved:Delete", "s3:ObjectRemoved:DeleteMarkerCreated", "s3:LifecycleExpiration:Delete", "s3:LifecycleExpiration:DeleteMarkerCreated"}

func expandedNotificationEvents(events []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, event := range events {
		found := false
		for _, supported := range objectNotificationEvents {
			if event == supported || strings.HasSuffix(event, ":*") && strings.TrimSuffix(event, "*") == supported[:strings.LastIndex(supported, ":")+1] {
				set[supported], found = true, true
			}
		}
		if !found {
			return nil, errors.New("unsupported S3 notification event")
		}
	}
	if len(set) == 0 {
		return nil, errors.New("notification requires events")
	}
	return set, nil
}

// NormalizeObjectNotificationRules rejects ambiguous filters before replacement.
func NormalizeObjectNotificationRules(in []ObjectNotificationRule) ([]ObjectNotificationRule, error) {
	if len(in) > MaxObjectNotificationRules {
		return nil, errors.New("too many notification rules")
	}
	out := make([]ObjectNotificationRule, len(in))
	ids := map[string]bool{}
	eventSets := make([]map[string]bool, len(in))
	for i, r := range in {
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if !validObjectNotificationID(r.ID) || ids[r.ID] {
			return nil, errors.New("invalid or duplicate notification ID")
		}
		ids[r.ID] = true
		if _, err := ParseObjectNotificationTarget(r.Destination); err != nil {
			return nil, err
		}
		for _, f := range []string{r.Prefix, r.Suffix} {
			if !utf8.ValidString(f) || len(f) > MaxObjectNotificationFilterBytes || strings.ContainsAny(f, "*\x00") {
				return nil, errors.New("invalid notification filter")
			}
		}
		if len(r.Events) > MaxObjectNotificationEvents {
			return nil, errors.New("too many notification events")
		}
		set, err := expandedNotificationEvents(r.Events)
		if err != nil {
			return nil, err
		}
		eventSets[i] = set
		r.Events = append([]string(nil), r.Events...)
		sort.Strings(r.Events)
		for j := 1; j < len(r.Events); j++ {
			if r.Events[j] == r.Events[j-1] {
				return nil, errors.New("duplicate notification event")
			}
		}
		out[i] = r
		for j := 0; j < i; j++ {
			o := out[j]
			overlap := false
			for event := range set {
				overlap = overlap || eventSets[j][event]
			}
			if overlap && (strings.HasPrefix(r.Prefix, o.Prefix) || strings.HasPrefix(o.Prefix, r.Prefix)) && (strings.HasSuffix(r.Suffix, o.Suffix) || strings.HasSuffix(o.Suffix, r.Suffix)) {
				return nil, errors.New("overlapping notification filters for the same event")
			}
		}
	}
	return out, nil
}

func CloneObjectNotificationRules(in []ObjectNotificationRule) []ObjectNotificationRule {
	out := make([]ObjectNotificationRule, len(in))
	for i, r := range in {
		out[i] = r
		out[i].Events = append([]string(nil), r.Events...)
	}
	return out
}

func validObjectNotificationID(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxObjectNotificationIDRunes {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return s != ""
}

func ObjectNotificationEventName(typ string, d ObjectStorageEvent) string {
	if typ == ObjectEventCreated {
		switch d.Operation {
		case "put", "upload":
			return "s3:ObjectCreated:Put"
		case "copy":
			return "s3:ObjectCreated:Copy"
		case "complete_multipart_upload":
			return "s3:ObjectCreated:CompleteMultipartUpload"
		}
	}
	if typ != ObjectEventRemoved && typ != ObjectEventDeleteMarkerCreated {
		return ""
	}
	group := "s3:ObjectRemoved:"
	if d.Cause == "lifecycle" {
		group = "s3:LifecycleExpiration:"
	}
	if typ == ObjectEventDeleteMarkerCreated {
		return group + "DeleteMarkerCreated"
	}
	return group + "Delete"
}

func MatchObjectNotification(r ObjectNotificationRule, typ string, d ObjectStorageEvent) bool {
	set, err := expandedNotificationEvents(r.Events)
	return err == nil && set[ObjectNotificationEventName(typ, d)] && strings.HasPrefix(d.Key, r.Prefix) && strings.HasSuffix(d.Key, r.Suffix)
}
