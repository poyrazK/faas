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

func TestMemStorePublicStatusReratingAndVisibleCorrectionsPreserveOrdering(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	startsAt := time.Now().UTC().Add(-time.Hour)
	event, err := store.CreatePublicStatusEvent(ctx, StatusEventCreate{
		IdempotencyKey: "create-correctable", Actor: "operator@example.com", Kind: publicstatus.KindIncident,
		Title: "Elevated eror rate", Impact: publicstatus.StateDegraded,
		Components: []publicstatus.Component{publicstatus.ComponentAPIConsole},
		State:      publicstatus.LifecycleInvestigating, StartsAt: &startsAt, Message: "We are investigatng.",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalPostedAt := event.Updates[0].At
	major := publicstatus.StateMajorOutage
	event, err = store.AppendPublicStatusUpdate(ctx, event.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "rerate-major", Actor: "operator@example.com", State: publicstatus.LifecycleIdentified,
		Message: "The outage affects application networking.", Impact: &major,
		Components: []publicstatus.Component{publicstatus.ComponentNetworking}, At: event.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Impact != major || len(event.Components) != 1 || event.Components[0] != publicstatus.ComponentNetworking {
		t.Fatalf("event attribution = impact:%q components:%v", event.Impact, event.Components)
	}
	latest := event.Updates[len(event.Updates)-1]
	if latest.Impact == nil || *latest.Impact != major || len(latest.Components) != 1 {
		t.Fatalf("rerating missing from timeline: %#v", latest)
	}

	degraded := publicstatus.StateDegraded
	event, err = store.AppendPublicStatusUpdate(ctx, event.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "rerate-degraded", Actor: "operator@example.com", State: publicstatus.LifecycleMonitoring,
		Message: "Mitigation reduced the impact.", Impact: &degraded, At: event.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Impact != degraded || event.Components[0] != publicstatus.ComponentNetworking {
		t.Fatalf("omitted components changed attribution: impact:%q components:%v", event.Impact, event.Components)
	}
	orderedAt := event.UpdatedAt
	event, err = store.EditPublicStatusEventTitle(ctx, event.PublicID, StatusEventTitleEditInput{
		Actor: "operator@example.com", Title: "Elevated error rate", At: orderedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Title != "Elevated error rate" || event.EditedAt == nil || !event.UpdatedAt.Equal(orderedAt) {
		t.Fatalf("title correction changed ordering metadata: %#v", event)
	}

	event, err = store.AppendPublicStatusUpdate(ctx, event.PublicID, StatusEventUpdateInput{
		IdempotencyKey: "resolve-correctable", Actor: "operator@example.com", State: publicstatus.LifecycleResolved,
		Message: "Recovered.", At: orderedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminalUpdatedAt := event.UpdatedAt
	event, err = store.EditPublicStatusEventTitle(ctx, event.PublicID, StatusEventTitleEditInput{
		Actor: "operator@example.com", Title: "Elevated API error rate", At: terminalUpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("correct terminal event title: %v", err)
	}
	if !event.UpdatedAt.Equal(terminalUpdatedAt) {
		t.Fatalf("terminal title correction moved updated_at from %v to %v", terminalUpdatedAt, event.UpdatedAt)
	}
	titleEditedAt := *event.EditedAt
	event, err = store.EditPublicStatusEventTitle(ctx, event.PublicID, StatusEventTitleEditInput{
		Actor: "operator@example.com", Title: "Elevated API error rate", At: terminalUpdatedAt.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.EditedAt == nil || !event.EditedAt.Equal(titleEditedAt) {
		t.Fatalf("repeated title correction changed edited_at: got %v want %v", event.EditedAt, titleEditedAt)
	}
	event, err = store.EditPublicStatusUpdateMessage(ctx, event.PublicID, event.Updates[0].ID, StatusUpdateMessageEditInput{
		Actor: "operator@example.com", Message: "We are investigating.", At: terminalUpdatedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Updates[0].Message != "We are investigating." || event.Updates[0].EditedAt == nil || !event.Updates[0].At.Equal(originalPostedAt) {
		t.Fatalf("timeline correction changed posted ordering: %#v", event.Updates[0])
	}
	if !event.UpdatedAt.Equal(terminalUpdatedAt) {
		t.Fatalf("timeline correction moved event updated_at from %v to %v", terminalUpdatedAt, event.UpdatedAt)
	}
	messageEditedAt := *event.Updates[0].EditedAt
	event, err = store.EditPublicStatusUpdateMessage(ctx, event.PublicID, event.Updates[0].ID, StatusUpdateMessageEditInput{
		Actor: "operator@example.com", Message: "We are investigating.", At: terminalUpdatedAt.Add(6 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Updates[0].EditedAt == nil || !event.Updates[0].EditedAt.Equal(messageEditedAt) {
		t.Fatalf("repeated timeline correction changed edited_at: got %v want %v", event.Updates[0].EditedAt, messageEditedAt)
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
