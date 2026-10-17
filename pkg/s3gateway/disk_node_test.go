package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const diskTestCredential = "local-provider-test-credential"

func diskFixture(t *testing.T) (*objectnode.Store, *DiskHandler) {
	t.Helper()
	store, err := objectnode.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1", MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	return store, h
}

func diskClient(server *httptest.Server) *awss3.Client {
	return awss3.New(awss3.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(diskTestCredential, diskTestCredential, ""), HTTPClient: server.Client(), RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
}

func TestDiskNodeAWSClient(t *testing.T) {
	_, handler := diskFixture(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := diskClient(server)
	bucket := aws.String("test-bucket")
	if _, err := client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: bucket}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"folder/a file+ç.txt", "folder/b.txt", "z.txt"} {
		_, err := client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: bucket, Key: aws.String(key), Body: strings.NewReader("0123456789"), ContentType: aws.String("text/plain"), CacheControl: aws.String("max-age=60"), Metadata: map[string]string{"owner": "test"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	key := aws.String("folder/a file+ç.txt")
	head, err := client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: bucket, Key: key})
	if err != nil || aws.ToInt64(head.ContentLength) != 10 || head.Metadata["owner"] != "test" || aws.ToString(head.CacheControl) != "max-age=60" {
		t.Fatal("HEAD lost metadata", head, err)
	}
	get, err := client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: bucket, Key: key, Range: aws.String("bytes=2-5")})
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(get.Body)
	get.Body.Close()
	if readErr != nil || string(body) != "2345" || aws.ToString(get.ContentRange) != "bytes 2-5/10" {
		t.Fatal("range GET failed", string(body), get.ContentRange, readErr)
	}
	first, err := client.ListObjectsV2(t.Context(), &awss3.ListObjectsV2Input{Bucket: bucket, Prefix: aws.String("folder/"), MaxKeys: aws.Int32(1), EncodingType: types.EncodingTypeUrl})
	if err != nil || len(first.Contents) != 1 || !aws.ToBool(first.IsTruncated) {
		t.Fatal(first, err)
	}
	second, err := client.ListObjectsV2(t.Context(), &awss3.ListObjectsV2Input{Bucket: bucket, Prefix: aws.String("folder/"), MaxKeys: aws.Int32(1), EncodingType: types.EncodingTypeUrl, ContinuationToken: first.NextContinuationToken})
	if err != nil || len(second.Contents) != 1 || aws.ToBool(second.IsTruncated) || aws.ToString(second.Contents[0].Key) != "folder%2Fb.txt" {
		t.Fatal("listing cursor failed", second, err)
	}
	_, err = client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: bucket, Key: key, Body: strings.NewReader("wrong"), IfNoneMatch: aws.String("*")})
	assertSDKErrorCode(t, err, "PreconditionFailed")
	_, err = client.DeleteBucket(t.Context(), &awss3.DeleteBucketInput{Bucket: bucket})
	assertSDKErrorCode(t, err, "BucketNotEmpty")
	_, err = client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: bucket, Key: aws.String("copy"), CopySource: aws.String("test-bucket/folder/b.txt")})
	assertSDKErrorCode(t, err, "NotImplemented")
}

func TestDiskNodeSignedCapabilitiesAndIntegrity(t *testing.T) {
	store, handler := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := diskClient(server)
	signer := awss3.NewPresignClient(client)
	put, err := signer.PresignPutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("signed"), ContentLength: aws.Int64(4), Metadata: map[string]string{"receipt": "private"}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, put.URL, strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = put.SignedHeader.Clone()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("presigned PUT failed", response.StatusCode)
	}
	tampered, err := http.NewRequestWithContext(t.Context(), http.MethodPut, put.URL, strings.NewReader("evil"))
	if err != nil {
		t.Fatal(err)
	}
	tampered.Header = put.SignedHeader.Clone()
	tampered.Header.Set("X-Amz-Meta-Added", "unsigned")
	denied, err := server.Client().Do(tampered)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatal("presigned PUT accepted unsigned metadata")
	}
	_, err = client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("signed"), Body: strings.NewReader("wrong"), ContentMD5: aws.String("AAAAAAAAAAAAAAAAAAAAAA==")})
	assertSDKErrorCode(t, err, "BadDigest")
	_, err = client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("signed"), Body: strings.NewReader("wrong"), ServerSideEncryption: types.ServerSideEncryptionAes256})
	assertSDKErrorCode(t, err, "NotImplemented")
	get, err := client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("signed")})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if string(body) != "data" || get.Metadata["receipt"] != "private" {
		t.Fatal("failed writes changed the object or its metadata", string(body), get.Metadata)
	}
	unsigned, err := server.Client().Get(server.URL + "/test-bucket/signed")
	if err != nil {
		t.Fatal(err)
	}
	unsigned.Body.Close()
	if unsigned.StatusCode != 403 {
		t.Fatal("unsigned request was accepted")
	}
}

func TestDiskNodeVerifiesSignedPayloadBeforePublication(t *testing.T) {
	store, handler := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://node.test/test-bucket/key", strings.NewReader("changed"))
	expected := sha256.Sum256([]byte("original"))
	payload := hex.EncodeToString(expected[:])
	request.Header.Set("X-Amz-Content-Sha256", payload)
	err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, request, payload, "s3", "us-east-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "BadDigest") {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, _, err = store.Get("test-bucket", "key"); !errors.Is(err, objectnode.ErrNoObject) {
		t.Fatal("tampered payload was published", err)
	}
}

func TestDiskNodeThroughBrandedGateway(t *testing.T) {
	store, node := diskFixture(t)
	if err := store.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	f := newMultipartCopyIntegrationWithTransfer(t, state.NewMemStore(), node, objectstorage.Config{}, 0)
	for _, body := range []string{"persisted through the public gateway", ""} {
		_, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key +ç"), Body: strings.NewReader(body), Metadata: map[string]string{"owner": "customer"}})
		if err != nil {
			t.Fatalf("gateway → S3 driver → disk node PUT: %v", err)
		}
		out, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("key +ç")})
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(out.Body)
		out.Body.Close()
		if readErr != nil || string(data) != body || out.Metadata["owner"] != "customer" {
			t.Fatal("gateway GET lost object data", string(data), readErr)
		}
	}
	listed, err := f.client.ListObjectsV2(t.Context(), &awss3.ListObjectsV2Input{Bucket: aws.String("assets")})
	if err != nil || len(listed.Contents) != 1 || aws.ToString(listed.Contents[0].Key) != "key +ç" {
		t.Fatal("gateway listing failed", listed, err)
	}
	if _, err = f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String("key +ç")}); err != nil {
		t.Fatal("gateway journaled deletion failed", err)
	}
	_, err = f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("key +ç")})
	assertSDKErrorCode(t, err, "NoSuchKey")
}
