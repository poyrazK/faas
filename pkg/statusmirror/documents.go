package statusmirror

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
)

// PublishStatus writes the status snapshot. Writes are unconditional: every
// apid replica may publish, and the newest evaluation wins.
func PublishStatus(ctx context.Context, store Store, publisher string, overview api.PublicStatusOverview, now time.Time) error {
	body, err := json.Marshal(StatusDocument{Schema: SchemaVersion, PublishedAt: now.UTC(), Publisher: publisher, Status: overview})
	if err != nil {
		return err
	}
	return store.Put(ctx, StatusKey, body, StatusCacheControl, "")
}

// maxNotices bounds the document. Resolved notices beyond the limit are
// dropped oldest first; open notices are never dropped.
const maxNotices = 50

// resolvedRetention keeps resolved notices visible for a while after an
// outage so readers can see what happened while the platform was down.
const resolvedRetention = 7 * 24 * time.Hour

// ReadNotices returns the notices document and its ETag. A missing object
// is an empty document with ETag "-", which makes the next write create-only.
func ReadNotices(ctx context.Context, store Store) (NoticesDocument, string, error) {
	object, err := store.Get(ctx, NoticesKey)
	if errors.Is(err, ErrNotFound) {
		return NoticesDocument{Schema: SchemaVersion, Events: []api.PublicStatusEvent{}}, "-", nil
	}
	if err != nil {
		return NoticesDocument{}, "", err
	}
	var doc NoticesDocument
	if err := json.Unmarshal(object.Body, &doc); err != nil {
		return NoticesDocument{}, "", fmt.Errorf("status mirror notices are not valid JSON: %w", err)
	}
	if doc.Schema != SchemaVersion {
		return NoticesDocument{}, "", fmt.Errorf("status mirror notices schema %d is not supported", doc.Schema)
	}
	if doc.Events == nil {
		doc.Events = []api.PublicStatusEvent{}
	}
	return doc, object.ETag, nil
}

// NoticeInput is a new offline incident.
type NoticeInput struct {
	Title      string
	Impact     string
	Components []string
	State      string
	Message    string
	StartsAt   time.Time
}

// NoticeUpdate changes an existing offline incident.
type NoticeUpdate struct {
	ID      string
	State   string
	Message string
}

// PostNotice validates and appends an offline incident, retrying the
// read-modify-write if another operator changed the document meanwhile.
func PostNotice(ctx context.Context, store Store, in NoticeInput, now time.Time) (api.PublicStatusEvent, error) {
	components := make([]publicstatus.Component, len(in.Components))
	for i, component := range in.Components {
		components[i] = publicstatus.Component(component)
	}
	startsAt := in.StartsAt.UTC()
	if verr := publicstatus.ValidateEvent(publicstatus.EventInput{
		Kind: publicstatus.KindIncident, Title: in.Title, Impact: publicstatus.State(in.Impact), Components: components,
		State: publicstatus.Lifecycle(in.State), StartsAt: &startsAt,
	}); verr != nil {
		return api.PublicStatusEvent{}, verr
	}
	if verr := publicstatus.ValidateMessage(in.Message); verr != nil {
		return api.PublicStatusEvent{}, verr
	}
	now = now.UTC()
	impact := in.Impact
	event := api.PublicStatusEvent{
		ID: newID(), Kind: string(publicstatus.KindIncident), Title: in.Title, Impact: in.Impact,
		Components: append([]string(nil), in.Components...), State: in.State, StartsAt: &startsAt, UpdatedAt: now,
		Updates: []api.PublicStatusUpdate{{ID: newID(), State: in.State, Message: in.Message, PostedAt: now, Impact: &impact, Components: append([]string(nil), in.Components...)}},
	}
	err := modifyNotices(ctx, store, now, func(doc *NoticesDocument) error {
		doc.Events = append(doc.Events, event)
		return nil
	})
	return event, err
}

// UpdateNotice appends an update to an offline incident. State "resolved"
// closes it.
func UpdateNotice(ctx context.Context, store Store, in NoticeUpdate, now time.Time) (api.PublicStatusEvent, error) {
	if verr := publicstatus.ValidateMessage(in.Message); verr != nil {
		return api.PublicStatusEvent{}, verr
	}
	now = now.UTC()
	var updated api.PublicStatusEvent
	err := modifyNotices(ctx, store, now, func(doc *NoticesDocument) error {
		for i := range doc.Events {
			event := &doc.Events[i]
			if event.ID != in.ID {
				continue
			}
			next := in.State
			if next == "" {
				next = event.State
			}
			if verr := publicstatus.ValidateTransition(publicstatus.KindIncident, publicstatus.Lifecycle(event.State), publicstatus.Lifecycle(next)); verr != nil {
				return verr
			}
			event.State = next
			event.UpdatedAt = now
			if next == string(publicstatus.LifecycleResolved) {
				event.ResolvedAt = &now
			}
			event.Updates = append(event.Updates, api.PublicStatusUpdate{ID: newID(), State: next, Message: in.Message, PostedAt: now})
			updated = *event
			return nil
		}
		return fmt.Errorf("no offline notice with id %q", in.ID)
	})
	return updated, err
}

func modifyNotices(ctx context.Context, store Store, now time.Time, change func(*NoticesDocument) error) error {
	for attempt := 0; attempt < 5; attempt++ {
		doc, etag, err := ReadNotices(ctx, store)
		if err != nil {
			return err
		}
		if err := change(&doc); err != nil {
			return err
		}
		doc.Events = pruneNotices(doc.Events, now)
		doc.Schema, doc.UpdatedAt = SchemaVersion, now
		body, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		err = store.Put(ctx, NoticesKey, body, NoticesCacheControl, etag)
		if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}

// pruneNotices drops resolved notices past retention, then the oldest
// resolved ones beyond maxNotices. Open notices always stay.
func pruneNotices(events []api.PublicStatusEvent, now time.Time) []api.PublicStatusEvent {
	open := make([]api.PublicStatusEvent, 0, len(events))
	resolved := make([]api.PublicStatusEvent, 0, len(events))
	for _, event := range events {
		switch {
		case event.ResolvedAt == nil:
			open = append(open, event)
		case now.Sub(*event.ResolvedAt) <= resolvedRetention:
			resolved = append(resolved, event)
		}
	}
	sort.SliceStable(resolved, func(i, j int) bool { return resolved[i].ResolvedAt.After(*resolved[j].ResolvedAt) })
	if room := maxNotices - len(open); len(resolved) > room {
		if room < 0 {
			room = 0
		}
		resolved = resolved[:room]
	}
	return append(open, resolved...)
}

func newID() string {
	id, err := uuid.NewRandomFromReader(rand.Reader)
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
