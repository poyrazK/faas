package sched

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 552
func objectNotificationsSDKEndToEnd(t *testing.T, st objectEventIntegrationStore, restart func() objectEventIntegrationStore, account state.Account, b state.ObjectBucket, c *awss3.Client, qapp, fapp state.App) {
	t.Helper()
	ctx := t.Context()
	// The fixture's first native-version response correctly fences current-only
	// accounting. Supply its retained-version inventory before further writes.
	j, e := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = st.ClaimObjectCapacityReconciliation(ctx, j.ID, uuid.NewString())
	if e != nil || j.State != "scanning" {
		t.Fatal(j, e)
	}
	hash := sha256.Sum256([]byte("images/a.jpg\x00provider-private-event-version"))
	if _, e = st.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{{Identity: hex.EncodeToString(hash[:]), Bytes: 4}}); e != nil {
		t.Fatal(e)
	}
	qb, err := st.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: qapp.ID, Name: "storage", QueueName: "storage", Enabled: true, Mode: "pull", WorkloadClass: "worker", MaxConcurrency: 1, RetryPolicyJSON: json.RawMessage(`{"max_attempts":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.MustParse(account.ID).String()
	queueARN := "arn:gregale:sqs:us-east-1:" + accountID + ":" + qapp.ID + "/storage"
	functionARN := "arn:gregale:lambda:us-east-1:" + accountID + ":function:" + fapp.ID
	filter := func(prefix string) *types.NotificationConfigurationFilter {
		return &types.NotificationConfigurationFilter{Key: &types.S3KeyFilter{FilterRules: []types.FilterRule{{Name: types.FilterRuleNamePrefix, Value: aws.String(prefix)}}}}
	}
	config := types.NotificationConfiguration{QueueConfigurations: []types.QueueConfiguration{{Id: aws.String("queue-images"), QueueArn: aws.String(queueARN), Events: []types.Event{types.EventS3ObjectCreatedPut}, Filter: filter("queue/")}}, LambdaFunctionConfigurations: []types.LambdaFunctionConfiguration{{Id: aws.String("function-images"), LambdaFunctionArn: aws.String(functionARN), Events: []types.Event{types.EventS3ObjectCreatedPut}, Filter: filter("function/")}}}
	putConfig := func(n types.NotificationConfiguration) error {
		_, e := c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{Bucket: aws.String(b.Name), NotificationConfiguration: &n})
		return e
	}
	if err = putConfig(config); err != nil {
		t.Fatal(err)
	}
	out, err := c.GetBucketNotificationConfiguration(ctx, &awss3.GetBucketNotificationConfigurationInput{Bucket: aws.String(b.Name), ExpectedBucketOwner: aws.String(account.ID)})
	if err != nil || len(out.QueueConfigurations) != 1 || len(out.LambdaFunctionConfigurations) != 1 || aws.ToString(out.QueueConfigurations[0].QueueArn) != queueARN {
		t.Fatal(out, err)
	}
	bad := config
	bad.QueueConfigurations = append([]types.QueueConfiguration(nil), config.QueueConfigurations...)
	bad.QueueConfigurations[0].QueueArn = aws.String(strings.Replace(queueARN, accountID, uuid.NewString(), 1))
	if putConfig(bad) == nil {
		t.Fatal("foreign destination accepted")
	}
	after, err := c.GetBucketNotificationConfiguration(ctx, &awss3.GetBucketNotificationConfigurationInput{Bucket: aws.String(b.Name)})
	if err != nil || aws.ToString(after.QueueConfigurations[0].QueueArn) != queueARN {
		t.Fatal("failed replacement changed policy", after, err)
	}
	fillers := []string{}
	for i := 0; i < api.MustLimitsFor(account.Plan).MaxQueueDepth; i++ {
		row, e := st.EnqueueInvocation(ctx, state.Invocation{AppID: qapp.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: qb.QueueName, Payload: json.RawMessage(`{}`), DueAt: time.Now()})
		if e != nil {
			t.Fatal(e)
		}
		fillers = append(fillers, row.ID)
	}
	key := "queue/red flower+%?.jpg"
	put, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key), Body: strings.NewReader("body")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(b.Name), Key: aws.String("function/new.jpg"), Body: strings.NewReader("body")}); err != nil {
		t.Fatal(err)
	}
	queueWork, err := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var qrecipient state.PublishedEventRecipient
	for _, r := range queueWork.RecipientSnapshot {
		if r.ObjectNotification != nil {
			qrecipient = r
		}
	}
	if qrecipient.ObjectNotification == nil || qrecipient.ObjectNotification.QueueBindingID != qb.ID {
		t.Fatal("queue destination not snapshotted", queueWork)
	}
	loop := &Loop{engine: &Engine{store: st}}
	routeErr := loop.routePublishedEventSnapshot(ctx, queueWork)
	if routeErr == nil {
		t.Fatal("full queue accepted notification")
	}
	if err = st.FinishPublishedEvent(ctx, queueWork.ID, queueWork.ClaimToken, routeErr); err != nil {
		t.Fatal(err)
	}
	// Clear intent after both acceptances, before either successful delivery.
	if err = putConfig(types.NotificationConfiguration{}); err != nil {
		t.Fatal(err)
	}
	empty, err := c.GetBucketNotificationConfiguration(ctx, &awss3.GetBucketNotificationConfigurationInput{Bucket: aws.String(b.Name)})
	if err != nil || len(empty.QueueConfigurations)+len(empty.LambdaFunctionConfigurations) != 0 {
		t.Fatal(empty, err)
	}
	functionWork, err := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = loop.routePublishedEventSnapshot(ctx, functionWork); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishPublishedEvent(ctx, functionWork.ID, functionWork.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	functionRows, err := st.ListInvocationsForApp(ctx, fapp.ID)
	if err != nil || len(functionRows) != 1 || functionRows[0].Source != state.InvocationAsyncInvoke {
		t.Fatal("captured function target", functionRows, err)
	}
	if err = st.CancelInvocation(ctx, fillers[0]); err != nil {
		t.Fatal(err)
	}
	st = restart()
	work, err := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour))
	if err != nil || work.ID != queueWork.ID {
		t.Fatal("retry receipt", work, err)
	}
	faults := &objectDeliveryFaultStore{Store: st, PublishedEventWorkStore: st, PublishedEventRecipientProgressStore: st, ObjectNotificationStore: st, progressRecipient: qrecipient.ID}
	loop = &Loop{engine: &Engine{store: faults}}
	routeErr = loop.routePublishedEventSnapshot(ctx, work)
	if routeErr == nil || !faults.progressFailed {
		t.Fatal("checkpoint loss not exercised", routeErr)
	}
	if err = st.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, routeErr); err != nil {
		t.Fatal(err)
	}
	st = restart()
	loop = &Loop{engine: &Engine{store: st}}
	work, err = st.ClaimDuePublishedEvent(ctx, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = loop.routePublishedEventSnapshot(ctx, work); err != nil {
		t.Fatal("idempotent admission at full depth", err)
	}
	if err = st.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListInvocationsForApp(ctx, qapp.ID)
	if err != nil {
		t.Fatal(err)
	}
	received := 0
	for _, row := range rows {
		if len(row.Payload) < len(`{"Records":`) || !strings.Contains(string(row.Payload), `"Records"`) {
			continue
		}
		received++
		var msg objectNotificationMessage
		if err = json.Unmarshal(row.Payload, &msg); err != nil || len(msg.Records) != 1 {
			t.Fatal(msg, err)
		}
		rec := msg.Records[0]
		decoded, e := url.QueryUnescape(rec.S3.Object.Key)
		if e != nil || decoded != key || rec.Name != "ObjectCreated:Put" || rec.S3.ConfigurationID != "queue-images" || rec.S3.Bucket.Name != b.Name || rec.S3.Object.VersionID != aws.ToString(put.VersionId) || rec.S3.Object.ETag != "actual" || rec.S3.Object.Size == nil || *rec.S3.Object.Size != 4 || row.Source != state.InvocationQueue || row.QueueName != "storage" {
			t.Fatal("wrong S3 queue record", row, rec, e)
		}
		if strings.Contains(string(row.Payload), "provider-private") || strings.Contains(string(row.Payload), "physical") {
			t.Fatal("private provider identity", string(row.Payload))
		}
	}
	if received != 1 {
		t.Fatal("duplicate or missing queue notification", received)
	}
}
