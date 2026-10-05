package s3gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type bucketEncryptionNativeHTTP struct {
	mu            sync.Mutex
	body          string
	puts, deletes int
}

func (f *bucketEncryptionNativeHTTP) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	switch r.Method {
	case http.MethodGet:
		if f.body == "" {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
			return
		}
		_, _ = io.WriteString(w, f.body)
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		f.puts++
		f.body = string(body)
	case http.MethodDelete:
		f.deletes++
		f.body = ""
		w.WriteHeader(204)
	default:
		t.Error("unexpected native configuration request", r.Method)
	}
}

// adr: 561
func TestBucketEncryptionSDKEndToEndMem(t *testing.T) { bucketEncryptionSDKE2E(t, state.NewMemStore()) }
func TestBucketEncryptionSDKEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	bucketEncryptionSDKE2E(t, st)
}

func bucketEncryptionSDKE2E(t *testing.T, st multipartCopyIntegrationStore) {
	native := &publicEncryptionHTTP{objects: map[string]publicEncryptionObject{}, uploads: map[string]publicEncryptionObject{}}
	configuration := &bucketEncryptionNativeHTTP{}
	f := newMultipartCopyIntegrationConfigured(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("encryption") {
			configuration.serve(t, w, r)
			return
		}
		native.serve(t, w, r)
	}), objectstorage.Config{}, 0, func(account, endpoint string) objectstorage.EncryptionConfig {
		return objectstorage.EncryptionConfig{KMSEndpoint: endpoint, Algorithms: []string{"AES256", "aws:kms", "aws:kms:dsse"}, Keys: []objectstorage.EncryptionKeyBinding{{ID: "11111111-1111-4111-8111-111111111111", AccountID: uuid.MustParse(account).String(), ProviderKeyID: publicEncryptionNativeKey}}}
	})
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	owned := backend.Encryption.Keys[0].Reference
	for _, algorithm := range []string{"AES256", "aws:kms", "aws:kms:dsse"} {
		t.Run(algorithm, func(t *testing.T) {
			var key *string
			if algorithm != "AES256" {
				key = aws.String(owned)
			}
			rule := types.ServerSideEncryptionRule{ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{SSEAlgorithm: types.ServerSideEncryption(algorithm), KMSMasterKeyID: key}}
			if algorithm == "aws:kms" {
				rule.BucketKeyEnabled = aws.Bool(true)
			}
			_, err := f.client.PutBucketEncryption(t.Context(), &awss3.PutBucketEncryptionInput{Bucket: aws.String("assets"), ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{Rules: []types.ServerSideEncryptionRule{rule}}})
			if err != nil {
				t.Fatal("configure default through SDK", err)
			}
			out, err := f.client.GetBucketEncryption(t.Context(), &awss3.GetBucketEncryptionInput{Bucket: aws.String("assets")})
			if err != nil || out.ServerSideEncryptionConfiguration == nil || len(out.ServerSideEncryptionConfiguration.Rules) != 1 {
				t.Fatal(out, err)
			}
			got := out.ServerSideEncryptionConfiguration.Rules[0]
			if string(got.ApplyServerSideEncryptionByDefault.SSEAlgorithm) != algorithm || aws.ToString(got.ApplyServerSideEncryptionByDefault.KMSMasterKeyID) != aws.ToString(key) {
				t.Fatal("native key escaped or default was lost", got)
			}
			if algorithm == "aws:kms" && !aws.ToBool(got.BucketKeyEnabled) {
				t.Fatal("configuration response lost bucket keys")
			}
			objectKey := "put-" + algorithm
			put, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String(objectKey), Body: strings.NewReader("hello")})
			if err != nil {
				t.Fatal("implicit encrypted PUT", err)
			}
			assertPublicCipher(t, put.ResultMetadata, algorithm, aws.ToString(key))
			copied, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("copy-" + algorithm), CopySource: aws.String("assets/" + objectKey), TaggingDirective: types.TaggingDirectiveReplace})
			if err != nil {
				t.Fatal("implicit encrypted copy", err)
			}
			assertPublicCipher(t, copied.ResultMetadata, algorithm, aws.ToString(key))
			init, err := f.client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart-" + algorithm)})
			if err != nil {
				t.Fatal("implicit encrypted initiation", err)
			}
			assertPublicCipher(t, init.ResultMetadata, algorithm, aws.ToString(key))
			part, err := f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("multipart-" + algorithm), UploadId: init.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("part")})
			if err != nil {
				t.Fatal(err)
			}
			done, err := f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart-" + algorithm), UploadId: init.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.ETag}}}})
			if err != nil {
				t.Fatal("default multipart completion", err)
			}
			assertPublicCipher(t, done.ResultMetadata, algorithm, aws.ToString(key))
			if algorithm == "aws:kms" && (!aws.ToBool(put.BucketKeyEnabled) || !aws.ToBool(copied.BucketKeyEnabled) || !aws.ToBool(init.BucketKeyEnabled) || !aws.ToBool(done.BucketKeyEnabled)) {
				t.Fatal("write response lost captured bucket keys")
			}
		})
	}
	// An explicit object selection overrides the verified bucket default.
	explicit, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("explicit"), Body: strings.NewReader("hello"), ServerSideEncryption: types.ServerSideEncryptionAes256})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicCipher(t, explicit.ResultMetadata, "AES256", "")
	f.handler.enabled = func() bool { return false }
	if _, err = f.client.GetBucketEncryption(t.Context(), &awss3.GetBucketEncryptionInput{Bucket: aws.String("assets")}); err != nil {
		t.Fatal("disabled ingress hid config", err)
	}
	if _, err = f.client.DeleteBucketEncryption(t.Context(), &awss3.DeleteBucketEncryptionInput{Bucket: aws.String("assets")}); err != nil {
		t.Fatal("disabled ingress blocked cleanup", err)
	}
	_, err = f.client.GetBucketEncryption(t.Context(), &awss3.GetBucketEncryptionInput{Bucket: aws.String("assets")})
	assertSDKErrorCode(t, err, "ServerSideEncryptionConfigurationNotFoundError")
	configuration.mu.Lock()
	puts, deletes := configuration.puts, configuration.deletes
	configuration.mu.Unlock()
	if puts != 3 || deletes != 1 {
		t.Fatal(fmt.Sprint("duplicate native configuration mutation ", puts, " ", deletes))
	}
}
