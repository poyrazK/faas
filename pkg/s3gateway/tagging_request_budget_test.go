package s3gateway

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type taggingBudgetMeter struct {
	state.ObjectStorageProviderUsageStore
	waiting     atomic.Int32
	allAdmitted chan struct{}
}

func (m *taggingBudgetMeter) ReserveObjectStorageGatewayRequest(ctx context.Context, bucket string, at time.Time, p api.ObjectStoragePolicy) error {
	return m.ObjectStorageProviderUsageStore.(state.ObjectStorageGatewayRequestStore).ReserveObjectStorageGatewayRequest(ctx, bucket, at, p)
}

func (m *taggingBudgetMeter) RecordObjectStorageProviderRequest(ctx context.Context, bucket string, at time.Time) error {
	if m.waiting.Add(1) == 8 {
		close(m.allAdmitted)
	}
	select {
	case <-m.allAdmitted:
	case <-ctx.Done():
		return ctx.Err()
	}
	return m.ObjectStorageProviderUsageStore.RecordObjectStorageProviderRequest(ctx, bucket, at)
}

func TestConcurrentTaggingReservesLastRequest(t *testing.T) {
	t.Run("memory", func(t *testing.T) { taggingRequestBudgetSuite(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		st, _ := multipartCopyPGStore(t)
		taggingRequestBudgetSuite(t, st)
	})
}

func taggingRequestBudgetSuite(t *testing.T, st multipartCopyIntegrationStore) {
	t.Helper()
	var nativeCalls atomic.Int32
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nativeCalls.Add(1)
		_, _ = io.WriteString(w, `<Tagging><TagSet/></Tagging>`)
	}), objectstorage.Config{}, 0)
	configureRequestBudget(t, f, 1)
	f.handler.requestMetrics = &taggingBudgetMeter{ObjectStorageProviderUsageStore: st, allAdmitted: make(chan struct{})}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String("key")})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	allowed := 0
	for err := range results {
		if err == nil {
			allowed++
		} else {
			assertConfigurationBudgetError(t, err)
		}
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	t.Logf("one remaining request: successful=%d native_calls=%d usage=%+v err=%v", allowed, nativeCalls.Load(), state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()), err)
	if err != nil || allowed != 1 || nativeCalls.Load() != 1 || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).RequestCount != 1 {
		t.Error("separate tag admission and recording spent the same remaining allowance repeatedly")
	}
}

func TestTaggingPreservesAuthorizationAdmission(t *testing.T) {
	st := state.NewMemStore()
	var calls atomic.Int32
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `<Tagging><TagSet/></Tagging>`)
	}), objectstorage.Config{}, 0)
	configureRequestBudget(t, f, 100)
	f.handler.registry.Accounting.MaxMonthlyAuthorizations = 1
	in := &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String("key")}
	if _, err := f.client.GetObjectTagging(t.Context(), in); err != nil {
		t.Fatal("last authorization allowance was refused", err)
	}
	_, err := f.client.GetObjectTagging(t.Context(), in)
	assertConfigurationBudgetError(t, err)
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	u := state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now())
	if err != nil || calls.Load() != 1 || u.RequestCount != 1 || u.Authorizations != 1 {
		t.Fatal("tagging skipped its authorization budget", calls.Load(), u, err)
	}
}
