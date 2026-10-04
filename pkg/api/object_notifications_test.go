package api

import (
	"strings"
	"testing"
)

// adr: 552
func TestObjectNotificationsFiltersAndEventNames(t *testing.T) {
	arn := "arn:gregale:lambda:us-east-1:11111111-1111-4111-8111-111111111111:function:22222222-2222-4222-8222-222222222222"
	r := ObjectNotificationRule{ID: "put", Destination: arn, Events: []string{"s3:ObjectCreated:*"}, Prefix: "images/", Suffix: ".jpg"}
	if _, e := NormalizeObjectNotificationRules([]ObjectNotificationRule{r}); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ operation, typ, cause, name string }{{"put", ObjectEventCreated, "customer", "s3:ObjectCreated:Put"}, {"upload", ObjectEventCreated, "customer", "s3:ObjectCreated:Put"}, {"copy", ObjectEventCreated, "customer", "s3:ObjectCreated:Copy"}, {"complete_multipart_upload", ObjectEventCreated, "customer", "s3:ObjectCreated:CompleteMultipartUpload"}, {"delete", ObjectEventRemoved, "customer", "s3:ObjectRemoved:Delete"}, {"delete", ObjectEventDeleteMarkerCreated, "customer", "s3:ObjectRemoved:DeleteMarkerCreated"}, {"delete", ObjectEventRemoved, "lifecycle", "s3:LifecycleExpiration:Delete"}, {"delete", ObjectEventDeleteMarkerCreated, "lifecycle", "s3:LifecycleExpiration:DeleteMarkerCreated"}} {
		d := ObjectStorageEvent{Key: "images/a.jpg", Operation: tc.operation, Cause: tc.cause}
		if got := ObjectNotificationEventName(tc.typ, d); got != tc.name {
			t.Fatal(got, tc)
		}
		if tc.typ == ObjectEventCreated && !MatchObjectNotification(r, tc.typ, d) {
			t.Fatal("wildcard missed creation", tc)
		}
	}
	for _, key := range []string{"images/a.png", "other/a.jpg"} {
		if MatchObjectNotification(r, ObjectEventCreated, ObjectStorageEvent{Key: key, Operation: "put"}) {
			t.Fatal("filter broadened", key)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ObjectNotificationRule)
	}{
		{"foreign-partition", func(r *ObjectNotificationRule) { r.Destination = strings.Replace(arn, ":gregale:", ":aws:", 1) }},
		{"unsupported", func(r *ObjectNotificationRule) { r.Events = []string{"s3:ObjectCreated:Post"} }},
		{"wildcard", func(r *ObjectNotificationRule) { r.Prefix = "images/*" }},
		{"oversized", func(r *ObjectNotificationRule) { r.Suffix = strings.Repeat("a", MaxObjectNotificationFilterBytes+1) }},
		{"repeated-event", func(r *ObjectNotificationRule) { r.Events = []string{"s3:ObjectCreated:Put", "s3:ObjectCreated:Put"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := r
			tc.mutate(&x)
			if _, e := NormalizeObjectNotificationRules([]ObjectNotificationRule{x}); e == nil {
				t.Fatal("invalid configuration accepted", x)
			}
		})
	}
	x := r
	x.ID = "other"
	x.Prefix = "images/thumbs/"
	if _, e := NormalizeObjectNotificationRules([]ObjectNotificationRule{r, x}); e == nil {
		t.Fatal("overlapping filters accepted")
	}
	x.Suffix = ".png"
	if _, e := NormalizeObjectNotificationRules([]ObjectNotificationRule{r, x}); e != nil {
		t.Fatal("disjoint suffixes rejected", e)
	}
	x.Suffix = ".jpg"
	x.Events = []string{"s3:ObjectRemoved:*"}
	if _, e := NormalizeObjectNotificationRules([]ObjectNotificationRule{r, x}); e != nil {
		t.Fatal("disjoint events rejected", e)
	}
}
