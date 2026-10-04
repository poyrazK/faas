package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrObjectNotificationInvalid  = errors.New("invalid notification configuration or destination")
	ErrObjectNotificationCapacity = errors.New("notification destination admission capacity exhausted")
)

type ObjectNotificationStore interface {
	GetObjectBucketNotifications(context.Context, string, string, string) (api.ObjectBucketNotifications, error)
	SetObjectBucketNotifications(context.Context, string, string, string, []api.ObjectNotificationRule) (api.ObjectBucketNotifications, error)
	EnqueueObjectNotification(context.Context, Invocation, ObjectNotificationSnapshot) error
}

// This is captured with the mutation, never resolved from a changed policy at delivery.
type ObjectNotificationSnapshot struct {
	Rule           api.ObjectNotificationRule `json:"rule"`
	BucketName     string                     `json:"bucket_name"`
	BucketRegion   string                     `json:"bucket_region"`
	QueueBindingID string                     `json:"queue_binding_id,omitempty"`
	RetryPolicy    json.RawMessage            `json:"retry_policy,omitempty"`
}

func cloneObjectNotificationSnapshot(s ObjectNotificationSnapshot) ObjectNotificationSnapshot {
	s.Rule.Events = append([]string(nil), s.Rule.Events...)
	s.RetryPolicy = bytes.Clone(s.RetryPolicy)
	return s
}

func notificationTarget(r api.ObjectNotificationRule, b ObjectBucket) (api.ObjectNotificationTarget, error) {
	t, err := api.ParseObjectNotificationTarget(r.Destination)
	if err != nil || !notificationSameIdentity(t.AccountID, b.AccountID) || t.Region != b.Region {
		return t, ErrObjectNotificationInvalid
	}
	return t, nil
}

func notificationEntitled(plan api.Plan, kind string) bool {
	l, ok := api.LimitsFor(plan)
	return ok && (kind == "queue" && l.MaxQueueDepth > 0 || kind == "function" && l.AsyncInvokeAllowed)
}

func notificationRecipients(b ObjectBucket, p api.ObjectBucketNotifications, typ string, d api.ObjectStorageEvent, queue func(api.ObjectNotificationTarget) (string, []byte, error)) ([]PublishedEventRecipient, error) {
	out := []PublishedEventRecipient{}
	for _, r := range p.Rules {
		if !api.MatchObjectNotification(r, typ, d) {
			continue
		}
		t, err := notificationTarget(r, b)
		if err != nil {
			return nil, err
		}
		s := ObjectNotificationSnapshot{Rule: r, BucketName: b.Name, BucketRegion: b.Region}
		if t.Kind == "queue" {
			s.QueueBindingID, s.RetryPolicy, err = queue(t)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
		identity, _ := json.Marshal([]string{b.ID, strconv.FormatInt(p.Revision, 10), r.ID})
		out = append(out, PublishedEventRecipient{ID: uuid.NewSHA1(uuid.NameSpaceURL, identity).String(), AccountID: b.AccountID, AppID: t.AppID, Source: api.ObjectEventSource, Type: typ, Filter: json.RawMessage("{}"), WorkSnapshotCaptured: true, ObjectNotification: &s})
	}
	return out, nil
}

func validateNotificationInvocation(in Invocation, s ObjectNotificationSnapshot) (api.ObjectNotificationTarget, error) {
	t, err := api.ParseObjectNotificationTarget(s.Rule.Destination)
	if id, e := uuid.Parse(in.ID); e != nil || id.String() != in.ID {
		return t, ErrObjectNotificationInvalid
	}
	if err != nil || in.ID == "" || !notificationSameIdentity(in.AccountID, t.AccountID) || !notificationSameIdentity(in.AppID, t.AppID) || !json.Valid(in.Payload) || !json.Valid(in.Headers) || in.WorkPolicyName != "" {
		return t, ErrObjectNotificationInvalid
	}
	if t.Kind == "queue" && (in.Source != InvocationQueue || in.QueueName != t.QueueName) || t.Kind == "function" && (in.Source != InvocationAsyncInvoke || in.QueueName != "") {
		return t, ErrObjectNotificationInvalid
	}
	return t, nil
}

func notificationSameIdentity(a, b string) bool {
	x, err := uuid.Parse(a)
	y, e := uuid.Parse(b)
	return err == nil && e == nil && x == y
}

func decodeObjectNotificationPolicy(bucket string, revision int64, raw []byte) (api.ObjectBucketNotifications, error) {
	p := api.ObjectBucketNotifications{BucketID: bucket, Revision: revision}
	if err := json.Unmarshal(raw, &p.Rules); err != nil {
		return p, fmt.Errorf("decode notification configuration: %w", err)
	}
	for _, r := range p.Rules {
		if r.ID == "" {
			return p, ErrObjectNotificationInvalid
		}
	}
	var err error
	p.Rules, err = api.NormalizeObjectNotificationRules(p.Rules)
	return p, err
}
