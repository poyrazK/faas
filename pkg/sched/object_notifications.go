package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectNotificationMessage struct {
	Records []objectNotificationRecord `json:"Records"`
}
type objectNotificationRecord struct {
	EventVersion string               `json:"eventVersion"`
	EventSource  string               `json:"eventSource"`
	Region       string               `json:"awsRegion"`
	Time         time.Time            `json:"eventTime"`
	Name         string               `json:"eventName"`
	S3           objectNotificationS3 `json:"s3"`
}
type objectNotificationS3 struct {
	SchemaVersion   string                   `json:"s3SchemaVersion"`
	ConfigurationID string                   `json:"configurationId"`
	Bucket          objectNotificationBucket `json:"bucket"`
	Object          objectNotificationObject `json:"object"`
}
type objectNotificationBucket struct {
	Name          string            `json:"name"`
	ARN           string            `json:"arn"`
	OwnerIdentity map[string]string `json:"ownerIdentity"`
}
type objectNotificationObject struct {
	Key       string `json:"key"`
	Size      *int64 `json:"size,omitempty"`
	ETag      string `json:"eTag,omitempty"`
	VersionID string `json:"versionId,omitempty"`
}

// Receipt confirmation has no authoritative provider ordering, so this profile
// deliberately omits sequencer rather than inventing one from a random ID.
func objectNotificationPayload(envelope events.Envelope, s state.ObjectNotificationSnapshot, d api.ObjectStorageEvent) ([]byte, error) {
	r := objectNotificationRecord{EventVersion: "2.1", EventSource: "aws:s3", Region: s.BucketRegion, Time: envelope.Time, Name: strings.TrimPrefix(api.ObjectNotificationEventName(envelope.Type, d), "s3:")}
	r.S3 = objectNotificationS3{SchemaVersion: "1.0", ConfigurationID: s.Rule.ID, Bucket: objectNotificationBucket{Name: s.BucketName, ARN: "arn:gregale:s3:" + s.BucketRegion + ":" + envelope.AccountID + ":bucket:" + d.BucketID, OwnerIdentity: map[string]string{"principalId": envelope.AccountID}}, Object: objectNotificationObject{Key: url.QueryEscape(d.Key), Size: d.SizeBytes, ETag: strings.Trim(d.ETag, "\""), VersionID: d.VersionID}}
	return json.Marshal(objectNotificationMessage{Records: []objectNotificationRecord{r}})
}

func (l *Loop) routeObjectNotification(ctx context.Context, envelope events.Envelope, row state.PublishedEventRecipient, now time.Time) (bool, error) {
	s := row.ObjectNotification
	var d api.ObjectStorageEvent
	if envelope.Source != api.ObjectEventSource || envelope.AccountID != row.AccountID || json.Unmarshal(envelope.Data, &d) != nil {
		return false, eventWorkRouteError(row.ID, "validate object event", state.ErrObjectNotificationInvalid, false)
	}
	if !api.MatchObjectNotification(s.Rule, envelope.Type, d) {
		return false, nil
	}
	payload, err := objectNotificationPayload(envelope, *s, d)
	if err != nil {
		return true, eventWorkRouteError(row.ID, "encode S3 notification", err, false)
	}
	t, err := api.ParseObjectNotificationTarget(s.Rule.Destination)
	if err != nil {
		return false, eventWorkRouteError(row.ID, "decode captured target", err, false)
	}
	headers, _ := json.Marshal(map[string]string{"x-gregale-event-id": envelope.ID, "x-gregale-event-source": envelope.Source, "x-gregale-event-type": envelope.Type, "x-gregale-event-subscription-id": row.ID})
	in := state.Invocation{ID: state.PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, row.ID), AppID: row.AppID, AccountID: row.AccountID, Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: eventInvocationMethod, Path: eventInvocationPath, Payload: payload, Headers: headers, DueAt: now}
	if t.Kind == "queue" {
		in.Source, in.QueueName = state.InvocationQueue, t.QueueName
	}
	st, ok := l.engine.store.(state.ObjectNotificationStore)
	if !ok {
		return true, eventWorkRouteError(row.ID, "notification admission unavailable", state.ErrObjectNotificationCapacity, true)
	}
	if err = st.EnqueueObjectNotification(ctx, in, *s); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return true, fmt.Errorf("notification %s target unavailable: %w", row.ID, &eventFanoutRouteError{code: state.EventFanoutFailureCodeTargetUnavailable, err: state.ErrNotFound})
		}
		return true, eventWorkRouteError(row.ID, "admit object notification", err, true)
	}
	if l.pool != nil {
		_ = db.Notify(ctx, l.pool, db.NotifyInvocationDue, fmt.Sprintf(`{"invocation_id":"%s","app_id":"%s","source":"%s"}`, in.ID, in.AppID, in.Source))
	}
	return true, nil
}
