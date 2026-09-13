package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/publicstatus"
)

func TestMemStorePublicStatusEventIdempotencyAndTimeline(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	create := StatusEventCreate{
		IdempotencyKey: "create-incident-1", Actor: "operator@example.com",
		Kind: publicstatus.KindIncident, Title: "Elevated API errors",
		Impact: publicstatus.StateDegraded, Components: []publicstatus.Component{publicstatus.ComponentAPIConsole},
		State: publicstatus.LifecycleInvestigating, StartsAt: &now,
		Message: "We are investigating elevated errors.",
	}
	first, err := store.CreatePublicStatusEvent(ctx, create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := store.CreatePublicStatusEvent(ctx, create)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if first.PublicID == "" || second.PublicID != first.PublicID {
		t.Fatalf("replay IDs = %q and %q, want one stable public UUID", first.PublicID, second.PublicID)
	}

	identified, err := store.AppendPublicStatusUpdate(ctx, first.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "update-incident-1", Actor: "operator@example.com",
		State: publicstatus.LifecycleIdentified, Message: "The API database pool is saturated.", At: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("append update: %v", err)
	}
	if identified.State != publicstatus.LifecycleIdentified || len(identified.Updates) != 2 {
		t.Fatalf("updated event = %#v, want identified with two chronological entries", identified)
	}
	if identified.Updates[0].At.After(identified.Updates[1].At) {
		t.Fatalf("updates not chronological: %#v", identified.Updates)
	}

	replay, err := store.AppendPublicStatusUpdate(ctx, first.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "update-incident-1", Actor: "operator@example.com",
		State: publicstatus.LifecycleMonitoring, Message: "different replay body", At: now.Add(20 * time.Minute),
	})
	if err != nil {
		t.Fatalf("update replay: %v", err)
	}
	if replay.State != publicstatus.LifecycleIdentified || len(replay.Updates) != 2 {
		t.Fatalf("idempotency key mutated event: %#v", replay)
	}
}

func TestMemStorePublicStatusTerminalEventCannotReopen(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	event, err := store.CreatePublicStatusEvent(ctx, StatusEventCreate{
		IdempotencyKey: "create-2", Actor: "operator@example.com", Kind: publicstatus.KindIncident,
		Title: "Build outage", Impact: publicstatus.StateMajorOutage,
		Components: []publicstatus.Component{publicstatus.ComponentDeployments},
		State:      publicstatus.LifecycleInvestigating, StartsAt: &now, Message: "Builds are failing.",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendPublicStatusUpdate(ctx, event.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "resolve-2", Actor: "operator@example.com",
		State: publicstatus.LifecycleResolved, Message: "Builds have recovered.", At: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendPublicStatusUpdate(ctx, event.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "reopen-2", Actor: "operator@example.com",
		State: publicstatus.LifecycleMonitoring, Message: "Reopening.", At: now.Add(2 * time.Hour),
	})
	var validation *publicstatus.ValidationError
	if !errors.As(err, &validation) || validation.Code != publicstatus.CodeTerminalEvent {
		t.Fatalf("reopen error = %#v, want terminal-event validation error", err)
	}
}

func TestMemStoreStatusBucketsDeduplicateFiveMinuteTimestamp(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	bucket := time.Date(2026, 9, 9, 10, 2, 20, 0, time.UTC)
	if err := store.RecordStatusBucket(ctx, StatusBucket{
		Component: publicstatus.ComponentNetworking, BucketAt: bucket,
		State: publicstatus.StateOperational, HasTelemetry: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStatusBucket(ctx, StatusBucket{
		Component: publicstatus.ComponentNetworking, BucketAt: bucket.Add(90 * time.Second),
		State: publicstatus.StatePartialOutage, HasTelemetry: true,
	}); err != nil {
		t.Fatal(err)
	}
	buckets, err := store.ListStatusBuckets(ctx, bucket.Add(-time.Hour), bucket.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].State != publicstatus.StateOperational || buckets[0].BucketAt.Minute() != 0 {
		t.Fatalf("buckets = %#v, want first write retained at 10:00 UTC", buckets)
	}
}

func TestMemStoreLegacyStatusWritesPopulatePublicShape(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	created, err := store.InsertStatusIncident(ctx, StatusIncidentComponentFaasControlPlane, StatusIncidentSeverityFullOutage, "Control plane unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if created.PublicID == "" || created.Kind != publicstatus.KindIncident || created.Impact != publicstatus.StateMajorOutage {
		t.Fatalf("legacy create did not populate public fields: %#v", created)
	}
	if len(created.Components) != len(publicstatus.AllComponents()) || len(created.Updates) != 1 {
		t.Fatalf("legacy control-plane mapping = %#v, want all capabilities and one initial update", created)
	}
	if err := store.ResolveStatusIncident(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.StatusEventByPublicID(ctx, created.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != publicstatus.LifecycleResolved || resolved.ResolvedAt == nil || len(resolved.Updates) != 2 {
		t.Fatalf("legacy resolve did not append a public update: %#v", resolved)
	}
}
