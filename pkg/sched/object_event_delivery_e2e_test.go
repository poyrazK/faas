package sched

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectEventIntegrationStore interface {
	state.Store
	state.ObjectBucketStore
	state.ObjectStorageAccountingStore
	state.ObjectStorageProviderUsageStore
	state.ObjectS3CredentialStore
	state.EventSubscriptionStore
	state.PublishedEventWorkStore
	state.PublishedEventRecipientProgressStore
}

type objectDeliveryFaultStore struct {
	state.Store
	state.PublishedEventWorkStore
	state.PublishedEventRecipientProgressStore
	enqueueApp, progressRecipient string
	enqueueFailed, progressFailed bool
}

func (s *objectDeliveryFaultStore) EnqueueInvocation(ctx context.Context, in state.Invocation) (state.Invocation, error) {
	if in.AppID == s.enqueueApp && !s.enqueueFailed {
		s.enqueueFailed = true
		return state.Invocation{}, errors.New("injected transient admission failure")
	}
	return s.Store.EnqueueInvocation(ctx, in)
}
func (s *objectDeliveryFaultStore) RecordPublishedEventRecipientProgress(ctx context.Context, id int64, token, recipient string, p state.PublishedEventRecipientProgress) error {
	if recipient == s.progressRecipient && !s.progressFailed {
		s.progressFailed = true
		return errors.New("injected progress acknowledgment loss")
	}
	return s.PublishedEventRecipientProgressStore.RecordPublishedEventRecipientProgress(ctx, id, token, recipient, p)
}

// adr: 409
func TestObjectEventDeliveryEndToEndMem(t *testing.T) {
	st := state.NewMemStore()
	objectEventDeliveryEndToEnd(t, st, func() objectEventIntegrationStore { return st })
}
func TestObjectEventDeliveryEndToEndPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	objectEventDeliveryEndToEnd(t, state.NewPgStore(pool), func() objectEventIntegrationStore { return state.NewPgStore(pool) })
}

func objectEventDeliveryEndToEnd(t *testing.T, st objectEventIntegrationStore, restart func() objectEventIntegrationStore) {
	ctx := t.Context()
	account, err := st.CreateAccount(ctx, "object-events-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.CreateAccount(ctx, "foreign-events-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := func(accountID, name string) state.App {
		t.Helper()
		a, e := st.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: name + uuid.NewString(), Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	source := app(account.ID, "source-")
	targets := []state.App{app(account.ID, "good-"), app(account.ID, "retry-"), app(account.ID, "progress-"), app(account.ID, "filtered-"), app(foreign.ID, "foreign-"), app(account.ID, "late-")}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.Header.Get("Authorization") == "" && !r.URL.Query().Has("X-Amz-Signature") {
			t.Error("unexpected provider request", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		if _, e := io.Copy(io.Discard, r.Body); e != nil {
			t.Error(e)
		}
		w.Header().Set("ETag", `"actual"`)
		w.Header().Set("X-Amz-Version-Id", "provider-private-event-version")
	}))
	t.Cleanup(upstream.Close)
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "events", Endpoint: upstream.URL, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}}}, func(string) string { return "local-test-provider" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: source.ID, Name: "assets", Scope: "default", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account.ID, source.ID, b.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "initial", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "local-provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const access = "GRGAAAAAAAAAAAAAAAAA"
	const secret = "0123456789012345678901234567890123456789"
	if _, err = st.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: account.ID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "test", Label: "events", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, 10); err != nil {
		t.Fatal(err)
	}
	filter, err := json.Marshal(map[string]any{"data": map[string]any{"bucket_id": b.ID, "key": map[string]string{"$prefix": "images/", "$suffix": ".jpg"}}})
	if err != nil {
		t.Fatal(err)
	}
	subscriptions := make([]state.EventSubscription, 0, 5)
	for i, target := range targets[:5] {
		selected := filter
		if i == 3 {
			selected = json.RawMessage(`{"data":{"key":{"$prefix":"archive/"}}}`)
		}
		sub, _, e := st.UpsertEventSubscription(ctx, target.AccountID, target.ID, api.ObjectEventSource, "object.*", selected)
		if e != nil {
			t.Fatal(e)
		}
		subscriptions = append(subscriptions, sub)
	}
	edge := httptest.NewUnstartedServer(nil)
	gateway, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: st, RequestMetrics: st, Host: edge.Listener.Addr().String(), Region: "us-east-1", HTTPClient: upstream.Client(), SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func([]byte) (string, error) { return secret, nil }})
	if err != nil {
		edge.Close()
		t.Fatal(err)
	}
	edge.Config.Handler = gateway
	edge.Start()
	t.Cleanup(edge.Close)
	client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(access, secret, ""), HTTPClient: edge.Client()}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(edge.URL)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
	put, err := client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("images/a.jpg"), Body: strings.NewReader("body")})
	if err != nil {
		t.Fatal(err)
	}
	if !state.ValidObjectVersionID(aws.ToString(put.VersionId)) || aws.ToString(put.VersionId) == "provider-private-event-version" {
		t.Fatal("private version result", put)
	}
	// A dead scheduler holds the claim. No notification wake is sent in this test.
	lost, err := st.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(lost.RecipientSnapshot) != 4 {
		t.Fatal("foreign or missing recipient", lost.RecipientSnapshot)
	}
	if err = st.DeleteEventSubscription(ctx, subscriptions[0].ID, account.ID, targets[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = st.UpsertEventSubscription(ctx, account.ID, targets[5].ID, api.ObjectEventSource, "object.*", filter); err != nil {
		t.Fatal(err)
	}
	st = restart()
	faults := &objectDeliveryFaultStore{Store: st, PublishedEventWorkStore: st, PublishedEventRecipientProgressStore: st, enqueueApp: targets[1].ID, progressRecipient: subscriptions[2].ID}
	loop := &Loop{engine: &Engine{store: faults}}
	work, err := st.ClaimDuePublishedEvent(ctx, lost.LeaseUntil.Add(time.Second))
	if err != nil || work.ID != lost.ID || work.ClaimToken == lost.ClaimToken {
		t.Fatal("claim recovery", work, err)
	}
	routeErr := loop.routePublishedEventSnapshot(ctx, work)
	if routeErr == nil || !faults.enqueueFailed || !faults.progressFailed {
		t.Fatal("faults not exercised", routeErr)
	}
	if err = st.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, routeErr); err != nil {
		t.Fatal(err)
	}
	// Reconstruction preserves progress and reuses the invocation whose
	// enqueue committed before the recipient checkpoint acknowledgment was lost.
	st = restart()
	faults.Store, faults.PublishedEventWorkStore, faults.PublishedEventRecipientProgressStore = st, st, st
	work, err = st.ClaimDuePublishedEvent(ctx, lost.LeaseUntil.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if work.RecipientProgress[subscriptions[0].ID].State != state.PublishedEventRecipientEnqueued {
		t.Fatal("successful recipient was forgotten", work)
	}
	loop = &Loop{engine: &Engine{store: faults}}
	if err = loop.routePublishedEventSnapshot(ctx, work); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	for i, target := range targets {
		rows, e := st.ListInvocationsForApp(ctx, target.ID)
		if e != nil {
			t.Fatal(e)
		}
		want := 0
		if i < 3 {
			want = 1
		}
		if len(rows) != want {
			t.Fatalf("target %d deliveries = %d, want %d", i, len(rows), want)
		}
		if want == 0 {
			continue
		}
		var envelope events.Envelope
		if e = json.Unmarshal(rows[0].Payload, &envelope); e != nil {
			t.Fatal(e)
		}
		var data api.ObjectStorageEvent
		if e = json.Unmarshal(envelope.Data, &data); e != nil {
			t.Fatal(e)
		}
		if envelope.Type != api.ObjectEventCreated || data.BucketID != b.ID || data.Key != "images/a.jpg" || data.VersionID != aws.ToString(put.VersionId) || data.ETag != aws.ToString(put.ETag) || data.SizeBytes == nil || *data.SizeBytes != 4 {
			t.Fatal(envelope, data)
		}
		for _, private := range []string{"provider-private", "physical", "sealed", secret} {
			if strings.Contains(string(rows[0].Payload), private) {
				t.Fatal("private event payload", private)
			}
		}
	}
	if _, err = st.ClaimDuePublishedEvent(ctx, lost.LeaseUntil.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("delivered event remained pending", err)
	}
	if calls.Load() != 1 {
		t.Fatal("delivery retried a provider write", calls.Load())
	}
}
