package state

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// This wire type mirrors events.Envelope without importing the event router,
// which itself depends on state. Publication happens inside the mutation's
// transaction (or MemStore lock), never after returning an HTTP response.
type objectEventEnvelope struct {
	SpecVersion     string                 `json:"specversion"`
	ID              string                 `json:"id"`
	Source          string                 `json:"source"`
	Type            string                 `json:"type"`
	Time            time.Time              `json:"time"`
	DataContentType string                 `json:"datacontenttype"`
	AccountID       string                 `json:"accountid"`
	Data            api.ObjectStorageEvent `json:"data"`
	Producer        string                 `json:"producer,omitempty"`
}

func trackedUploadEvent(c ObjectUploadCompletion) api.ObjectStorageEvent {
	return api.ObjectStorageEvent{ReceiptID: c.ID, BucketID: c.BucketID,
		AppID: c.AppID, Key: c.Key, Operation: ViewObjectWriteReceipt(c).Operation,
		Cause: "customer", VersionID: c.VersionID, ETag: c.ETag, SizeBytes: &c.Bytes}
}

func multipartCompletionEvent(u ObjectMultipartUpload) api.ObjectStorageEvent {
	return api.ObjectStorageEvent{ReceiptID: u.ID, BucketID: u.BucketID,
		AppID: u.AppID, Key: u.Key, Operation: "complete_multipart_upload",
		Cause: "customer", VersionID: u.CompletionVersionID,
		ETag: u.CompletionETag, SizeBytes: &u.SizeBytes}
}

func deletionEvent(j ObjectDeletion) (string, api.ObjectStorageEvent) {
	typ := api.ObjectEventRemoved
	if j.Selector == "" && j.DeleteMarker {
		typ = api.ObjectEventDeleteMarkerCreated
	}
	cause := "customer"
	if j.Lifecycle != nil {
		cause = "lifecycle"
	}
	return typ, api.ObjectStorageEvent{ReceiptID: j.ID, BucketID: j.BucketID,
		AppID: j.AppID, Key: j.Key, Operation: "delete", Cause: cause,
		VersionID: j.VersionID, DeleteMarker: j.DeleteMarker}
}

func objectEventPayload(account, identity, typ string, data api.ObjectStorageEvent, now time.Time) ([]byte, error) {
	envelope := objectEventEnvelope{SpecVersion: "1.0", ID: identity,
		Source: api.ObjectEventSource, Type: typ, Time: now.UTC(),
		DataContentType: "application/json", AccountID: account, Data: data}
	if data.AppID != "" {
		// A bucket's events stay inside its app's company (ADR-20261025211616).
		envelope.Producer = eventcontract.AppProducer(data.AppID)
	}
	return json.Marshal(envelope)
}

func publishObjectEventTx(ctx context.Context, db sqlc.DBTX, account, identity, typ string, data api.ObjectStorageEvent, now time.Time) error {
	payload, err := objectEventPayload(account, identity, typ, data, now)
	if err != nil {
		return err
	}
	if err = sqlc.New().ObjectMutationEventAppend(ctx, db, sqlc.ObjectMutationEventAppendParams{
		AccountID: mustPgUUID(account), Payload: payload, At: objectUsageTime(now)}); err != nil {
		return mapErr(err)
	}
	return captureObjectNotifications(ctx, db, account, identity, typ, data, payload)
}

func (m *MemStore) publishObjectEventLocked(account, identity, typ string, data api.ObjectStorageEvent, now time.Time) error {
	payload, err := objectEventPayload(account, identity, typ, data, now)
	if err != nil {
		return err
	}
	b := m.objectBuckets[data.BucketID]
	recipients, err := notificationRecipients(b, m.objectNotifications[b.ID], typ, data, m.notificationQueueLocked)
	if err != nil {
		return err
	}
	// The same company reach as subscriptions (ADR-20261025211616).
	if data.AppID != "" {
		if recipients, err = m.recipientsInProducerReachLocked(account, eventcontract.AppProducer(data.AppID), recipients); err != nil {
			return err
		}
	}
	accountUUID, err := uuid.Parse(account)
	if err != nil {
		return err
	}
	key := accountUUID.String() + "\x00" + api.ObjectEventSource + "\x00" + identity
	previous := m.eventFanout[key]
	if err = m.appendEventLocked("objectstorage", "event.published", &account, payload, nil, now); err != nil {
		return err
	}
	if previous != nil || len(recipients) == 0 {
		return nil
	}
	// The notification keeps an entry even when no subscription received
	// the event, as ObjectNotificationsCapture inserts it.
	work := m.platformEventWorkLocked(key, payload, now)
	work.RecipientSnapshot = append(work.RecipientSnapshot, recipients...)
	return nil
}

// platformEventWorkLocked is the outbox entry of an object event appended
// just now, created when enqueuePublishedEventLocked kept none because no
// subscription received it, as ObjectNotificationsCapture inserts the row.
func (m *MemStore) platformEventWorkLocked(key string, payload []byte, now time.Time) *PublishedEventWork {
	if work := m.eventFanout[key]; work != nil {
		return work
	}
	if m.eventFanout == nil {
		m.eventFanout = make(map[string]*PublishedEventWork)
	}
	m.eventFanoutNextID++
	work := &PublishedEventWork{ID: m.eventFanoutNextID, Payload: bytes.Clone(payload), SnapshotCaptured: true,
		RecipientProgress: make(map[string]PublishedEventRecipientProgress), AvailableAt: now, CreatedAt: time.Now().UTC()}
	m.eventFanout[key] = work
	return work
}
