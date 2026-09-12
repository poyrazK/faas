package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreObjectUploadRoutesLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	if _, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{AccountID: "acct", AppID: "app", Name: "invalid"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid route error = %v, want conflict", err)
	}

	zRoute := ObjectUploadRoute{
		ID: "route-z", AccountID: "acct", AppID: "app", Name: "z-route", BucketID: "bucket-z",
		KeyPrefix: "uploads/", MaxBytes: 1024, AllowedContentTypes: []string{"image/png"}, Enabled: true,
	}
	gotZ, err := m.UpsertObjectUploadRoute(ctx, zRoute)
	if err != nil {
		t.Fatal(err)
	}
	if gotZ.CreatedAt.IsZero() || gotZ.UpdatedAt.IsZero() {
		t.Fatalf("route timestamps = %+v", gotZ)
	}

	aRoute := ObjectUploadRoute{
		ID: "route-a", AccountID: "acct", AppID: "app", Name: "a-route", BucketID: "bucket-a",
		MaxBytes: 2048, AllowedContentTypes: []string{"text/plain"}, CreatedAt: createdAt,
	}
	if _, err := m.UpsertObjectUploadRoute(ctx, aRoute); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-other", AccountID: "acct", AppID: "app", Name: "a-route", BucketID: "bucket-other", MaxBytes: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate route error = %v, want conflict", err)
	}

	list, err := m.ListObjectUploadRoutes(ctx, "acct", "app")
	if err != nil || len(list) != 2 || list[0].Name != "a-route" || list[1].Name != "z-route" {
		t.Fatalf("sorted routes = %+v, err=%v", list, err)
	}
	list[1].AllowedContentTypes[0] = "mutated"
	fetched, err := m.GetObjectUploadRoute(ctx, "acct", "app", "z-route")
	if err != nil || fetched.AllowedContentTypes[0] != "image/png" {
		t.Fatalf("route was not defensively copied: %+v, err=%v", fetched, err)
	}
	if _, err := m.GetObjectUploadRoute(ctx, "other", "app", "z-route"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-account lookup error = %v, want not found", err)
	}

	updated, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-z", AccountID: "acct", AppID: "app", Name: "z-route", BucketID: "bucket-z",
		MaxBytes: 4096, AllowedContentTypes: []string{"image/jpeg"}, CreatedAt: time.Unix(1, 0),
	})
	if err != nil || !updated.CreatedAt.Equal(gotZ.CreatedAt) || updated.MaxBytes != 4096 {
		t.Fatalf("updated route = %+v, err=%v", updated, err)
	}

	completion, err := m.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{RouteID: "route-z", Key: "uploads/file", Bytes: 3})
	if !errors.Is(err, ErrConflict) || completion.ID != "" {
		t.Fatalf("invalid completion = %+v, err=%v", completion, err)
	}
	completion, err = m.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{ID: "completion-1", RouteID: "route-z", Key: "uploads/file", Bytes: 3})
	if err != nil || completion.CreatedAt.IsZero() {
		t.Fatalf("completion = %+v, err=%v", completion, err)
	}

	if err := m.DeleteObjectUploadRoute(ctx, "acct", "app", "z-route"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteObjectUploadRoute(ctx, "acct", "app", "z-route"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat delete error = %v, want not found", err)
	}
}

func TestMemStoreStatusHistoryRollupsAndIncidentWindow(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	day1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)
	since := day1.Add(-time.Hour)
	failure := OutcomeFailed
	success := OutcomeSuccess
	m.invocations = map[string]Invocation{
		"success":         {CreatedAt: day1.Add(time.Hour), State: InvocationPending, Outcome: &success},
		"failed":          {CreatedAt: day1.Add(2 * time.Hour), State: InvocationFailed},
		"outcome-failure": {CreatedAt: day1.Add(3 * time.Hour), State: InvocationFailed, Outcome: &failure},
		"cancelled":       {CreatedAt: day2.Add(time.Hour), State: InvocationCancelled},
		"dead-letter":     {CreatedAt: day2.Add(2 * time.Hour), State: InvocationDeadLetter},
		"pending":         {CreatedAt: day2.Add(3 * time.Hour), State: InvocationPending},
		"old":             {CreatedAt: since.Add(-time.Minute), State: InvocationCompleted},
	}
	buckets, err := m.StatusUptimeBuckets(ctx, since)
	if err != nil || len(buckets) != 2 {
		t.Fatalf("buckets = %+v, err=%v", buckets, err)
	}
	if !buckets[0].Day.Equal(day1) || buckets[0].Successful != 1 || buckets[0].Total != 3 {
		t.Fatalf("day1 bucket = %+v", buckets[0])
	}
	if !buckets[1].Day.Equal(day2) || buckets[1].Successful != 0 || buckets[1].Total != 2 {
		t.Fatalf("day2 bucket = %+v", buckets[1])
	}

	resolved := day1.Add(4 * time.Hour)
	m.statusIncidents = []StatusIncident{
		{ID: 1, Message: "old resolved", PostedAt: day1.Add(-2 * time.Hour), ResolvedAt: &resolved},
		{ID: 2, Message: "old open", PostedAt: day1.Add(-2 * time.Hour)},
		{ID: 3, Message: "recent", PostedAt: day2.Add(time.Hour)},
		{ID: 4, Message: "same time newer id", PostedAt: day2.Add(time.Hour)},
	}
	incidents, err := m.ListStatusIncidentsSince(ctx, day1, 0)
	if err != nil || len(incidents) != 3 || incidents[0].ID != 4 || incidents[1].ID != 3 || incidents[2].ID != 2 {
		t.Fatalf("incident window = %+v, err=%v", incidents, err)
	}
	limited, err := m.ListStatusIncidentsSince(ctx, day1, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != 4 {
		t.Fatalf("limited incidents = %+v, err=%v", limited, err)
	}
	if _, err := m.ListStatusIncidentsSince(ctx, day1, 101); err != nil {
		t.Fatal(err)
	}
}
