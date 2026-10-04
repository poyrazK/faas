package s3gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const publicEncryptionNativeKey = "arn:aws:kms:us-east-1:111122223333:key/abcd8987-12d6-45ad-a4bc-d384c10d9149"

type publicEncryptionObject struct {
	headers http.Header
	data    string
}
type publicEncryptionHTTP struct {
	mu                   sync.Mutex
	objects              map[string]publicEncryptionObject
	uploads              map[string]publicEncryptionObject
	requests, keys, puts int
	tamper, disabled     bool
}

func (f *publicEncryptionHTTP) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	key := path.Base(r.URL.Path)
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	if r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey" {
		f.keys++
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		enabled, stateName := true, "Enabled"
		if f.disabled {
			enabled, stateName = false, "Disabled"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"KeyMetadata": map[string]any{"Arn": publicEncryptionNativeKey, "AWSAccountId": "111122223333", "KeyId": "abcd8987-12d6-45ad-a4bc-d384c10d9149", "Enabled": enabled, "KeyState": stateName, "KeyUsage": "ENCRYPT_DECRYPT", "KeySpec": "SYMMETRIC_DEFAULT", "KeyManager": "CUSTOMER"}})
		return
	}
	switch {
	case r.Method == http.MethodGet && q.Has("uploads"):
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.checkCaptured(t, r)
		f.uploads[key] = publicEncryptionObject{headers: r.Header.Clone()}
		f.ack(w, r.Header)
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>private-%s</UploadId></InitiateMultipartUploadResult>`, key)
	case q.Get("uploadId") != "":
		obj, exists := f.uploads[key]
		if !exists {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("X-Amz-Server-Side-Encryption") != "" {
				t.Error("part supplied a new encryption policy")
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			obj.data = string(data)
			f.uploads[key] = obj
			w.Header().Set("ETag", `"encrypted"`)
		case http.MethodGet:
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;encrypted&quot;</ETag><Size>%d</Size></Part></ListPartsResult>`, len(obj.data))
		case http.MethodPost:
			f.objects[key] = obj
			delete(f.uploads, key)
			f.ack(w, obj.headers)
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;encrypted&quot;</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Error("unexpected upload method", r.Method)
			w.WriteHeader(500)
		}
	case r.Method == http.MethodPut:
		f.checkCaptured(t, r)
		f.puts++
		obj := publicEncryptionObject{headers: r.Header.Clone()}
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			source, err := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
			if err != nil {
				t.Error(err)
			}
			obj.data = f.objects[path.Base(source)].data
		} else {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			obj.data = string(data)
		}
		f.objects[key] = obj
		f.ack(w, obj.headers)
		w.Header().Set("ETag", `"encrypted"`)
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;encrypted&quot;</ETag><LastModified>2026-10-03T10:00:00Z</LastModified></CopyObjectResult>`)
		}
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		obj, exists := f.objects[key]
		if !exists {
			w.WriteHeader(404)
			return
		}
		f.ack(w, obj.headers)
		w.Header().Set("ETag", `"encrypted"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("Content-Type", "text/plain")
		for name, values := range obj.headers {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				w.Header()[name] = values
			}
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, obj.data)
		}
	default:
		t.Error("unexpected native request", r.Method, r.URL)
		w.WriteHeader(500)
	}
}
func (f *publicEncryptionHTTP) ack(w http.ResponseWriter, h http.Header) {
	for _, name := range []string{"X-Amz-Server-Side-Encryption", "X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "X-Amz-Server-Side-Encryption-Bucket-Key-Enabled"} {
		if value := h.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	if f.tamper {
		w.Header().Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "private-wrong-key")
	}
}
func (f *publicEncryptionHTTP) checkCaptured(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedObjectEncryptionMetadataKey) == "" || r.Header.Get("X-Amz-Server-Side-Encryption") == "" {
		t.Error("native request lost durable cipher proof")
	}
	if r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" && r.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != publicEncryptionNativeKey {
		t.Error("native request did not translate owned reference")
	}
}
func publicEncryptionFixture(t *testing.T, st multipartCopyIntegrationStore) (*multipartCopyIntegration, *publicEncryptionHTTP, string) {
	t.Helper()
	native := &publicEncryptionHTTP{objects: map[string]publicEncryptionObject{}, uploads: map[string]publicEncryptionObject{}}
	f := newMultipartCopyIntegrationConfigured(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }), objectstorage.Config{}, 0, func(account, endpoint string) objectstorage.EncryptionConfig {
		return objectstorage.EncryptionConfig{KMSEndpoint: endpoint, Algorithms: []string{"AES256", "aws:kms", "aws:kms:dsse"}, Keys: []objectstorage.EncryptionKeyBinding{{ID: "11111111-1111-4111-8111-111111111111", AccountID: uuid.MustParse(account).String(), ProviderKeyID: publicEncryptionNativeKey}}}
	})
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return f, native, backend.Encryption.Keys[0].Reference
}
func assertPublicCipher(t *testing.T, metadata middleware.Metadata, algorithm, key string) {
	t.Helper()
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	if !ok || response == nil {
		t.Fatal("missing SDK response")
	}
	h := response.Header
	if h.Get("X-Amz-Server-Side-Encryption") != algorithm || h.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != key {
		t.Fatal("owned response mismatch", h)
	}
	for name, values := range h {
		if strings.Contains(strings.Join(values, " "), publicEncryptionNativeKey) || strings.Contains(strings.ToLower(name), objectstorage.ReservedObjectEncryptionMetadataKey) {
			t.Fatal("private identity or proof escaped", name)
		}
	}
}

// adr: 414
func TestPublicEncryptionMem(t *testing.T) { publicEncryptionE2E(t, state.NewMemStore()) }
func TestPublicEncryptionPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	publicEncryptionE2E(t, st)
}
func publicEncryptionE2E(t *testing.T, st multipartCopyIntegrationStore) {
	f, native, key := publicEncryptionFixture(t, st)
	for _, algorithm := range []string{"AES256", "aws:kms", "aws:kms:dsse"} {
		t.Run(algorithm, func(t *testing.T) {
			var keyID, context *string
			if algorithm != "AES256" {
				keyID = aws.String(key)
				context = aws.String(base64.StdEncoding.EncodeToString([]byte(`{"purpose":"customer"}`)))
			}
			out, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("put"), Body: strings.NewReader("hello"), ServerSideEncryption: types.ServerSideEncryption(algorithm), SSEKMSKeyId: keyID, SSEKMSEncryptionContext: context})
			if err != nil {
				t.Fatal(err)
			}
			publicKey := aws.ToString(keyID)
			assertPublicCipher(t, out.ResultMetadata, algorithm, publicKey)
			copy, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("copy"), CopySource: aws.String("assets/put"), TaggingDirective: types.TaggingDirectiveReplace, ServerSideEncryption: types.ServerSideEncryption(algorithm), SSEKMSKeyId: keyID, SSEKMSEncryptionContext: context})
			if err != nil {
				t.Fatal(err)
			}
			assertPublicCipher(t, copy.ResultMetadata, algorithm, publicKey)
			for _, object := range []string{"put", "copy"} {
				head, err := f.client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: aws.String("assets"), Key: aws.String(object)})
				if err != nil {
					t.Fatal(err)
				}
				assertPublicCipher(t, head.ResultMetadata, algorithm, publicKey)
				get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(object)})
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(get.Body)
				_ = get.Body.Close()
				if err != nil || string(data) != "hello" {
					t.Fatal(string(data), err)
				}
				assertPublicCipher(t, get.ResultMetadata, algorithm, publicKey)
			}
			init, err := f.client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), ServerSideEncryption: types.ServerSideEncryption(algorithm), SSEKMSKeyId: keyID, SSEKMSEncryptionContext: context})
			if err != nil {
				t.Fatal(err)
			}
			assertPublicCipher(t, init.ResultMetadata, algorithm, publicKey)
			if _, err = uuid.Parse(aws.ToString(init.UploadId)); err != nil {
				t.Fatal("private upload ID", err)
			}
			part, err := f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), UploadId: init.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("hello"), ContentLength: aws.Int64(5)})
			if err != nil {
				t.Fatal(err)
			}
			done, err := f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), UploadId: init.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.ETag}}}})
			if err != nil {
				t.Fatal(err)
			}
			assertPublicCipher(t, done.ResultMetadata, algorithm, publicKey)
			u, err := st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, aws.ToString(init.UploadId))
			if err != nil || u.State != state.ObjectMultipartCompleted || u.Encryption.Selection.Algorithm != algorithm || u.Encryption.Selection.KeyID != publicKey {
				t.Fatal("durable selection not settled", u, err)
			}
		})
	}
	metrics, err := st.ListObjectStorageProviderRequestMetrics(t.Context(), f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	native.mu.Lock()
	defer native.mu.Unlock()
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(native.requests) || native.keys != 6 || native.puts != 6 {
		t.Fatal("actual native attempts not metered", metrics, err, native.requests, native.keys, native.puts)
	}
}
func TestPublicEncryptionRejectsForeignOrDisabledKey(t *testing.T) {
	f, native, key := publicEncryptionFixture(t, state.NewMemStore())
	for _, ref := range []string{publicEncryptionNativeKey, strings.Replace(key, uuid.MustParse(f.bucket.AccountID).String(), uuid.NewString(), 1)} {
		if _, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("bad"), Body: strings.NewReader("hello"), ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: aws.String(ref)}); err == nil {
			t.Fatal("unowned key accepted")
		}
	}
	native.mu.Lock()
	if native.requests != 0 {
		t.Error("invalid identity reached provider")
	}
	native.disabled = true
	native.mu.Unlock()
	if _, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("disabled"), Body: strings.NewReader("hello"), ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: aws.String(key)}); err == nil {
		t.Fatal("disabled key accepted")
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.puts != 0 || native.keys != 1 {
		t.Fatal("disabled key dispatched an object", native.puts, native.keys)
	}
}
