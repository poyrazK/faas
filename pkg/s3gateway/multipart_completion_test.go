package s3gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type conditionalGatewayProvider struct {
	*gatewayTestProvider
	conditions  []api.ObjectWriteConditions
	completeErr error
}

func (p *conditionalGatewayProvider) CompleteConditionalMultipartUpload(_ context.Context, _ string, _ objectstorage.MultipartCompleteRequest, c api.ObjectWriteConditions) error {
	p.conditions = append(p.conditions, c)
	return p.completeErr
}

func newConditionalGatewaySDK(t *testing.T) (*awss3.Client, *Handler, *gatewayMultipartStore, *conditionalGatewayProvider, string) {
	t.Helper()
	h, store, base := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, nil)
	p := &conditionalGatewayProvider{gatewayTestProvider: base}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, MaxUploadBytes: 16 << 20,
		Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "fixture", AllowedOrigins: []string{"https://console.example.test"}}},
	}, func(string) string { return "" }, map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	h.registry = registry
	h.now = func() time.Time { return time.Now().UTC() }
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	p.multipart = map[string]map[int32]objectstorage.MultipartPart{"provider": {1: {PartNumber: 1, ETag: `"part"`, SizeBytes: 4}}}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	h.host = endpoint.Host
	client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""), HTTPClient: server.Client()}, func(o *awss3.Options) {
		o.BaseEndpoint, o.UsePathStyle, o.RetryMaxAttempts = aws.String(server.URL), true, 1
	})
	return client, h, sessions, p, id
}

func conditionalCompleteInput(id string, c api.ObjectWriteConditions) *awss3.CompleteMultipartUploadInput {
	out := &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("key"), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String(`"part"`)}}}}
	if c.IfMatch != "" {
		out.IfMatch = aws.String(c.IfMatch)
	}
	if c.IfNoneMatch != "" {
		out.IfNoneMatch = aws.String(c.IfNoneMatch)
	}
	return out
}

func assertMultipartSDKError(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("err=%v, want %s", err, code)
	}
}

func TestGatewayConditionalMultipartSDKAndReplay(t *testing.T) {
	for _, c := range []api.ObjectWriteConditions{{IfNoneMatch: "*"}, {IfMatch: `"old"`}} {
		t.Run(c.IfMatch+c.IfNoneMatch, func(t *testing.T) {
			client, _, sessions, p, id := newConditionalGatewaySDK(t)
			input := conditionalCompleteInput(id, c)
			if _, err := client.CompleteMultipartUpload(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			u := sessions.uploads[id]
			if u.State != state.ObjectMultipartCompleted || u.CompletionConditions != c || len(p.conditions) != 1 || p.conditions[0] != c {
				t.Fatal(u, p.conditions)
			}
			if _, err := client.CompleteMultipartUpload(t.Context(), input); err != nil || len(p.conditions) != 1 {
				t.Fatal("replay repeated write", err, p.conditions)
			}
			_, err := client.CompleteMultipartUpload(t.Context(), conditionalCompleteInput(id, api.ObjectWriteConditions{}))
			assertMultipartSDKError(t, err, "OperationAborted")
			if len(p.conditions) != 1 || len(p.completedUploads) != 0 {
				t.Fatal("replay stripped condition")
			}
		})
	}
}

func TestGatewayConditionalMultipartFailureAndCleanup(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{objectstorage.ErrPreconditionFailed, "PreconditionFailed"}, {objectstorage.ErrConditionalConflict, "ConditionalRequestConflict"}, {objectstorage.ErrConditionalNotFound, "NoSuchKey"}} {
		t.Run(test.code, func(t *testing.T) {
			client, h, sessions, p, id := newConditionalGatewaySDK(t)
			p.completeErr = test.err
			input := conditionalCompleteInput(id, api.ObjectWriteConditions{IfMatch: `"old"`})
			_, err := client.CompleteMultipartUpload(t.Context(), input)
			assertMultipartSDKError(t, err, test.code)
			u := sessions.uploads[id]
			if u.State != state.ObjectMultipartAborting || u.CompletionErrorCode == "" {
				t.Fatal(u)
			}
			_, err = client.CompleteMultipartUpload(t.Context(), input)
			assertMultipartSDKError(t, err, test.code)
			if len(p.conditions) != 1 {
				t.Fatal("terminal rejection repeated provider write")
			}
			h.enabled = func() bool { return false }
			if _, err = client.AbortMultipartUpload(t.Context(), &awss3.AbortMultipartUploadInput{Bucket: input.Bucket, Key: input.Key, UploadId: input.UploadId}); err != nil {
				t.Fatal(err)
			}
			if sessions.uploads[id].State != state.ObjectMultipartAborted || sessions.uploads[id].CompletionErrorCode != u.CompletionErrorCode {
				t.Fatal("cleanup erased rejection")
			}
			h.enabled = func() bool { return true }
			_, err = client.CompleteMultipartUpload(t.Context(), input)
			assertMultipartSDKError(t, err, test.code)
		})
	}
}

func TestGatewayConditionalMultipartRetryDoesNotRelistParts(t *testing.T) {
	client, _, sessions, p, id := newConditionalGatewaySDK(t)
	c := api.ObjectWriteConditions{IfNoneMatch: "*"}
	p.completeErr = objectstorage.ErrUnavailable
	_, err := client.CompleteMultipartUpload(t.Context(), conditionalCompleteInput(id, c))
	assertMultipartSDKError(t, err, "ServiceUnavailable")
	u := sessions.uploads[id]
	if u.State != state.ObjectMultipartCompletingConditional || u.LeaseToken != "" || u.CompletionConditions != c {
		t.Fatal(u)
	}
	_, err = client.CompleteMultipartUpload(t.Context(), conditionalCompleteInput(id, api.ObjectWriteConditions{}))
	assertMultipartSDKError(t, err, "OperationAborted")
	p.multipartListErr = objectstorage.ErrNotFound
	p.completeErr = nil
	u.RetryAt = time.Now().Add(-time.Second)
	sessions.uploads[id] = u
	if _, err = client.CompleteMultipartUpload(t.Context(), conditionalCompleteInput(id, c)); err != nil {
		t.Fatal("lost-response retry required deleted upload parts", err)
	}
	if len(p.conditions) != 2 || p.conditions[1] != c || len(sessions.completionGrants) != 1 {
		t.Fatal("retry lost condition or readmitted capacity", p.conditions, sessions.completionGrants)
	}
}

func TestGatewayConditionalMultipartInvalidConditions(t *testing.T) {
	client, _, sessions, p, id := newConditionalGatewaySDK(t)
	_, err := client.CompleteMultipartUpload(t.Context(), conditionalCompleteInput(id, api.ObjectWriteConditions{IfMatch: `"old"`, IfNoneMatch: "*"}))
	assertMultipartSDKError(t, err, "InvalidArgument")
	if len(p.conditions) != 0 || sessions.uploads[id].State != state.ObjectMultipartActive {
		t.Fatal("invalid conditions mutated upload")
	}
}
