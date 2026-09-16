package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMemStoreObjectUploadRoutesCoverage(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	if _, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid route error = %v, want ErrConflict", err)
	}

	first, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-z", AccountID: "acct", AppID: "app", Name: "zeta", BucketID: "bucket",
		KeyPrefix: "uploads/", MaxBytes: 1024, AllowedContentTypes: []string{"text/plain"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert first route: %v", err)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Fatalf("insert timestamps = %+v, want populated", first)
	}

	second, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-a", AccountID: "acct", AppID: "app", Name: "alpha", BucketID: "bucket",
		MaxBytes: 2048, AllowedContentTypes: []string{"image/png"},
	})
	if err != nil {
		t.Fatalf("insert second route: %v", err)
	}
	if second.CreatedAt.IsZero() {
		t.Fatal("second route has no creation timestamp")
	}
	routes, err := m.ListObjectUploadRoutes(ctx, "acct", "app")
	if err != nil || len(routes) != 2 || routes[0].Name != "alpha" || routes[1].Name != "zeta" {
		t.Fatalf("list routes = %+v, err=%v; want alpha then zeta", routes, err)
	}
	routes[0].AllowedContentTypes[0] = "mutated"
	got, err := m.GetObjectUploadRoute(ctx, "acct", "app", "alpha")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	if !reflect.DeepEqual(got.AllowedContentTypes, []string{"image/png"}) {
		t.Fatalf("route was not defensively cloned: %+v", got.AllowedContentTypes)
	}
	if _, err := m.GetObjectUploadRoute(ctx, "other", "app", "alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account get error = %v, want ErrNotFound", err)
	}
	if got, err := m.ListObjectUploadRoutes(ctx, "other", "app"); err != nil || len(got) != 0 {
		t.Fatalf("cross-account list = %+v, err=%v; want empty", got, err)
	}

	// Updating an existing ID preserves its creation timestamp and replaces
	// the policy while a different ID with the same app/name is rejected.
	updated, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-z", AccountID: "acct", AppID: "app", Name: "zeta", BucketID: "bucket",
		KeyPrefix: "new/", MaxBytes: 4096, AllowedContentTypes: []string{"application/json"},
	})
	if err != nil || !updated.CreatedAt.Equal(first.CreatedAt) || updated.KeyPrefix != "new/" {
		t.Fatalf("updated route = %+v, err=%v; want preserved creation time", updated, err)
	}
	if _, err := m.UpsertObjectUploadRoute(ctx, ObjectUploadRoute{
		ID: "route-duplicate", AccountID: "acct", AppID: "app", Name: "zeta", BucketID: "bucket", MaxBytes: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate route error = %v, want ErrConflict", err)
	}
	if err := m.DeleteObjectUploadRoute(ctx, "acct", "app", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete error = %v, want ErrNotFound", err)
	}
	if err := m.DeleteObjectUploadRoute(ctx, "acct", "app", "alpha"); err != nil {
		t.Fatalf("delete route: %v", err)
	}

	if _, err := m.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid completion error = %v, want ErrConflict", err)
	}
	completion, err := m.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{
		ID: "completion-1", RouteID: "route-z", Key: "new/file", Bytes: 12,
	})
	if err != nil || completion.CreatedAt.IsZero() {
		t.Fatalf("completion = %+v, err=%v; want timestamp", completion, err)
	}
	created := time.Unix(123, 0).UTC()
	completion, err = m.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{
		ID: "completion-2", RouteID: "route-z", Key: "new/file-2", Bytes: 0, CreatedAt: created,
	})
	if err != nil || !completion.CreatedAt.Equal(created) {
		t.Fatalf("explicit completion timestamp = %+v, err=%v", completion, err)
	}
}
