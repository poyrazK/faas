package sched

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1036
// Uses the actual signed SDK/gateway and durable router, with a local provider.
func objectComputeTriggersSDKEndToEnd(t *testing.T, st objectEventIntegrationStore, restart func() objectEventIntegrationStore, b state.ObjectBucket, c *awss3.Client) {
	t.Helper()
	ctx := t.Context()
	s := st.(state.ObjectComputeTriggerStore)
	p, err := s.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal("read policy", err)
	}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &p.Revision, Triggers: []api.ObjectStorageComputeTrigger{{ID: "image-worker", Prefix: "compute/", Suffix: ".png"}}})
	if err != nil {
		t.Fatal("configure", err)
	}
	for _, key := range []string{"compute/input.png", "outputs/result.png"} {
		if _, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key), Body: strings.NewReader("body")}); err != nil {
			t.Fatal("upload", key, err)
		}
	}
	// The accepted route remains captured when deployment clears the live rule.
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &p.Revision, Triggers: []api.ObjectStorageComputeTrigger{}}); err != nil {
		t.Fatal("clear live intent", err)
	}
	work, err := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal("claim captured event", err)
	}
	var recipient state.PublishedEventRecipient
	for _, r := range work.RecipientSnapshot {
		if r.ObjectNotification != nil {
			recipient = r
		}
	}
	if recipient.ObjectNotification == nil || recipient.AppID != b.AppID {
		t.Fatal("owning app not captured", work)
	}
	faults := &objectDeliveryFaultStore{Store: st, PublishedEventWorkStore: st, PublishedEventRecipientProgressStore: st, ObjectNotificationStore: st, progressRecipient: recipient.ID}
	loop := &Loop{engine: &Engine{store: faults}}
	routeErr := loop.routePublishedEventSnapshot(ctx, work)
	if routeErr == nil || !faults.progressFailed {
		t.Fatal("checkpoint loss not exercised", routeErr)
	}
	if err = st.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, routeErr); err != nil {
		t.Fatal("finish lost checkpoint", err)
	}
	st = restart()
	loop = &Loop{engine: &Engine{store: st}}
	// Drain the nonmatching event and then retry the exact captured delivery.
	for i := 0; i < 2; i++ {
		next, e := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour))
		if e != nil {
			t.Fatal("claim retry", i, e)
		}
		if e = loop.routePublishedEventSnapshot(ctx, next); e != nil {
			t.Fatal("route retry", i, e)
		}
		// Atomic admission may settle a receipt containing only ordinary
		// subscriptions. The scheduler does not checkpoint it a second time.
		if !next.Delivered {
			if e = st.FinishPublishedEvent(ctx, next.ID, next.ClaimToken, nil); e != nil {
				t.Fatal("finish retry", i, e)
			}
		}
	}
	if _, err = st.ClaimDuePublishedEvent(ctx, time.Now().Add(2*time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("trigger receipts remain pending", err)
	}
	rows, err := st.ListInvocationsForApp(ctx, b.AppID)
	if err != nil || len(rows) != 1 || rows[0].Source != state.InvocationAsyncInvoke {
		t.Fatal("stable invocation admission", rows, err)
	}
	var msg objectNotificationMessage
	if err = json.Unmarshal(rows[0].Payload, &msg); err != nil || len(msg.Records) != 1 {
		t.Fatal(msg, err)
	}
	if !strings.Contains(string(rows[0].Payload), "compute%2Finput.png") {
		t.Fatal("wrong filtered object", string(rows[0].Payload))
	}
}
