package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func lifecycleCustomerClient(t *testing.T, f gatewayRecoveryFixture) (*server, *api.Client) {
	t.Helper()
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(t.Context(), f.account.ID, hash, "lifecycle", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return s, api.NewClient(srv.URL, token)
}

// adr: 550
func TestObjectLifecycleCustomerConfigurationPG(t *testing.T) {
	var requests atomic.Int32
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("policy configuration contacted provider", r.Method, r.URL)
		w.WriteHeader(500)
	}), 0)
	s, client := lifecycleCustomerClient(t, f)
	ctx, bucket := t.Context(), aws.String("assets")
	if p, err := client.GetObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID); err != nil || p.Revision != 0 || len(p.Rules) != 0 {
		t.Fatal(p, err)
	}
	if _, err := f.client.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{Bucket: bucket}); err == nil || !strings.Contains(err.Error(), "NoSuchLifecycleConfiguration") {
		t.Fatal(err)
	}
	days := int32(2)
	if _, err := f.client.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{Bucket: bucket, LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: []types.LifecycleRule{{Status: types.ExpirationStatusEnabled, Filter: &types.LifecycleRuleFilter{Prefix: aws.String("tmp/目录 /+%")}, AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{DaysAfterInitiation: &days}}}}}); err != nil {
		t.Fatal(err)
	}
	p, err := client.GetObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || p.Revision != 1 || len(p.Rules) != 1 || p.Rules[0].ID == "" || p.Rules[0].Filter.Prefix != "tmp/目录 /+%" {
		t.Fatal(p, err)
	}
	if _, err = client.PutObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID, api.ObjectBucketLifecycleRequest{Rules: p.Rules}); err != nil {
		t.Fatal(err)
	}
	j, err := client.CreateObjectLifecycleScan(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || j.State != "scanning" || j.Phase != "multipart" {
		t.Fatal(j, err)
	}
	if repeated, e := client.CreateObjectLifecycleScan(ctx, f.app.Slug, f.bucket.ID); e != nil || repeated.ID != j.ID {
		t.Fatal(repeated, e)
	}
	if _, err = f.st.ClaimObjectLifecycleScan(ctx, j.ID, "private-lease"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.DeleteObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID); err == nil {
		t.Fatal("cleared actively leased policy")
	}
	if err = f.st.RetryObjectLifecycleScan(ctx, j.ID, "private-lease"); err != nil {
		t.Fatal(err)
	}
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	f.enabled.Store(false)
	if _, err = client.PutObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID, api.ObjectBucketLifecycleRequest{Rules: p.Rules}); err == nil {
		t.Fatal("disabled policy PUT")
	}
	if read, e := client.GetObjectLifecycleScan(ctx, f.app.Slug, f.bucket.ID, j.ID); e != nil || read.ID != j.ID {
		t.Fatal(read, e)
	}
	read, e := f.client.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{Bucket: bucket})
	if e != nil || len(read.Rules) != 1 || aws.ToString(read.Rules[0].ID) != p.Rules[0].ID {
		t.Fatal(read, e)
	}
	if _, err = f.client.DeleteBucketLifecycle(ctx, &awss3.DeleteBucketLifecycleInput{Bucket: bucket}); err != nil {
		t.Fatal(err)
	}
	if cleared, e := client.GetObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID); e != nil || len(cleared.Rules) != 0 || cleared.Revision != 2 {
		t.Fatal(cleared, e)
	}
	if scan, e := client.GetObjectLifecycleScan(ctx, f.app.Slug, f.bucket.ID, j.ID); e != nil || scan.State != "cancelled" {
		t.Fatal(scan, e)
	}
	if _, err = client.DeleteObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("provider requests", requests.Load())
	}
}
