package objectstorage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func encryptionKeyResponse() string {
	return `{"KeyMetadata":{"Arn":"` + encryptionTestNativeKey + `","AWSAccountId":"111122223333","KeyId":"abcd8987-12d6-45ad-a4bc-d384c10d9149","Enabled":true,"KeyState":"Enabled","KeyUsage":"ENCRYPT_DECRYPT","KeySpec":"SYMMETRIC_DEFAULT","KeyManager":"CUSTOMER"}}`
}

func encryptionS3Fixture(t *testing.T, data http.HandlerFunc, keyResponse string) (*S3, Backend) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey" {
			var in struct {
				KeyID string `json:"KeyId"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil || in.KeyID != encryptionTestNativeKey || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/kms/aws4_request") {
				t.Error("incorrect KMS identity or signing service")
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_, _ = io.WriteString(w, keyResponse)
			return
		}
		data(w, r)
	}))
	t.Cleanup(server.Close)
	b := encryptionTestBackend()
	b.Endpoint, b.Encryption.KMSEndpoint, b.AllowHTTP = server.URL, server.URL, true
	_, placement := encryptionTestRegistry(t, b)
	return placement.Provider.(*S3), placement
}

func encryptionSelection(t *testing.T, b Backend, algorithm string) ResolvedObjectEncryption {
	t.Helper()
	selection := api.ObjectEncryption{Algorithm: algorithm}
	if algorithm != "AES256" {
		selection.KeyID = b.Encryption.Keys[0].Reference
		selection.Context = base64.StdEncoding.EncodeToString([]byte(`{"purpose":"local-fixture"}`))
	}
	resolved, err := b.Encryption.Resolve(encryptionTestAccount, selection)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func restartEncryptionS3(t *testing.T, p *S3) *S3 {
	t.Helper()
	b := encryptionTestBackend()
	b.Endpoint = aws.ToString(p.client.Options().BaseEndpoint)
	b.Encryption = cloneEncryptionConfig(p.encryption)
	b.AllowHTTP = true
	_, placement := encryptionTestRegistry(t, b)
	return placement.Provider.(*S3)
}

func encryptionFixtureHeaders(e ResolvedObjectEncryption) http.Header {
	h := http.Header{}
	h.Set("X-Amz-Server-Side-Encryption", e.Selection.Algorithm)
	if e.ProviderKeyID != "" {
		h.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", e.ProviderKeyID)
	}
	if e.Selection.BucketKeyEnabled != nil {
		h.Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", strconv.FormatBool(*e.Selection.BucketKeyEnabled))
	}
	return h
}

// adr: 554
func TestS3EncryptedPutPresignAndExactRecovery(t *testing.T) {
	for _, algorithm := range []string{"AES256", "aws:kms", "aws:kms:dsse"} {
		t.Run(algorithm, func(t *testing.T) {
			var mu sync.Mutex
			var stored http.Header
			var storedBytes string
			var e ResolvedObjectEncryption
			writes := 0
			p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodPut:
					writes++
					data, err := io.ReadAll(r.Body)
					if err != nil || string(data) != "abc" {
						t.Error("encrypted bytes changed", err)
					}
					stored, storedBytes = r.Header.Clone(), string(data)
					for name, values := range r.URL.Query() {
						if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
							stored.Set(name, strings.Join(values, ","))
						}
					}
					if !oneEncryptionHeader(stored, "X-Amz-Server-Side-Encryption", algorithm) || stored.Get("X-Amz-Server-Side-Encryption-Context") != e.Selection.Context || stored.Get("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey) == "" {
						t.Error("encrypted PUT omitted selection or proof")
					}
					for name, values := range stored {
						if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption") {
							w.Header()[name] = values
						}
					}
					w.Header().Set("ETag", `"exact-encrypted"`)
				case http.MethodHead:
					for name, values := range stored {
						if strings.HasPrefix(strings.ToLower(name), "x-amz-") {
							w.Header()[name] = values
						}
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(storedBytes)))
					w.Header().Set("ETag", `"exact-encrypted"`)
				default:
					t.Error("unexpected native request", r.Method, r.URL)
				}
			}, encryptionKeyResponse())
			e = encryptionSelection(t, placement, algorithm)
			if algorithm == "aws:kms" {
				e.Selection.BucketKeyEnabled = aws.Bool(true)
			}
			receipt := uuid.NewString()
			metadata := ObjectMetadata{ContentType: "text/plain", Metadata: map[string]string{"owner": "customer"}}
			result, err := p.WriteEncryptedObject(t.Context(), "physical", "key", receipt, strings.NewReader("abc"), 3, metadata, e)
			if err != nil || result.Encryption.KeyID != e.Selection.KeyID || result.Encryption.Algorithm != algorithm {
				t.Fatal("encrypted native PUT", result, err)
			}
			public, err := json.Marshal(result)
			if err != nil || strings.Contains(string(public), encryptionTestNativeKey) || strings.Contains(string(public), e.KeyIdentity) && e.KeyIdentity != "" {
				t.Fatal("operation result exposed private encryption binding")
			}
			if _, ok := metadata.Metadata[ReservedObjectEncryptionMetadataKey]; ok {
				t.Fatal("caller metadata mutated")
			}
			// Reconstruct the private dispatch snapshot and provider before recovery.
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			var restored ResolvedObjectEncryption
			if err = json.Unmarshal(raw, &restored); err != nil {
				t.Fatal(err)
			}
			restarted := restartEncryptionS3(t, p)
			if _, err = restarted.ConfirmEncryptedObject(t.Context(), "physical", "key", receipt, 3, restored); err != nil {
				t.Fatal("encrypted recovery", err)
			}
			foreign := e
			foreign.Selection.Context = base64.StdEncoding.EncodeToString([]byte(`{"purpose":"different"}`))
			if algorithm != "AES256" {
				if _, err = p.ConfirmEncryptedObject(t.Context(), "physical", "key", receipt, 3, foreign); !errors.Is(err, ErrUnavailable) {
					t.Fatal("recovery accepted different AAD", err)
				}
			}
			size := int64(3)
			signed, err := p.PresignEncryptedPut(t.Context(), "physical", SignRequest{Method: http.MethodPut, Key: "key", SizeBytes: &size, Metadata: metadata.Metadata}, ObjectWriteConditions{IfNoneMatch: "*"}, receipt, e)
			if err != nil {
				t.Fatal("encrypted native presign", err)
			}
			request, err := http.NewRequestWithContext(t.Context(), signed.Method, signed.URL, strings.NewReader("abc"))
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range signed.Headers {
				request.Header.Set(name, value)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil {
				t.Fatal("signed encrypted PUT execution", response.StatusCode, readErr, closeErr)
			}
			if _, err = p.ConfirmEncryptedObject(t.Context(), "physical", "key", receipt, 3, e); err != nil {
				t.Fatal("signed PUT proof", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if writes != 2 {
				t.Fatal("native PUT was retried", writes)
			}
		})
	}
}

func TestS3EncryptedCopyDoesNotInheritSourceEncryption(t *testing.T) {
	receipt := uuid.NewString()
	var e ResolvedObjectEncryption
	calls := atomic.Int32{}
	p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || !validEncryptionHeaders(r.Header, &e) || r.Header.Get("X-Amz-Server-Side-Encryption-Context") != e.Selection.Context || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` || r.Header.Get("X-Amz-Meta-Owner") != "source-customer" || r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey) != receipt || r.Header.Get("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey) != e.Proof() {
			t.Error("copy lost source fence or independent destination encryption")
		}
		for name, values := range encryptionFixtureHeaders(e) {
			w.Header()[name] = values
		}
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>"copy"</ETag></CopyObjectResult>`)
	}, encryptionKeyResponse())
	e = encryptionSelection(t, placement, "aws:kms")
	source := CopySourceSnapshot{SizeBytes: 3, ETag: `"source"`, Metadata: ObjectMetadata{ContentType: "text/plain", Metadata: map[string]string{"owner": "source-customer"}}}
	out, err := p.CopyEncryptedObject(t.Context(), "physical", receipt, CopyObjectRequest{SourceKey: "source", DestinationKey: "destination", MetadataDirective: "COPY"}, source, CopySourceConditions{}, e)
	if err != nil || out.ETag != `"copy"` || out.Encryption.KeyID != e.Selection.KeyID || calls.Load() != 1 {
		t.Fatal("encrypted tracked copy", out, err, calls.Load())
	}
	if source.Metadata.Metadata[ReservedObjectEncryptionMetadataKey] != "" {
		t.Fatal("source mutated")
	}
}

func TestS3EncryptedMultipartLostCompletionAndReconstruction(t *testing.T) {
	var mu sync.Mutex
	var metadata http.Header
	var e ResolvedObjectEncryption
	initiated, completed := false, false
	createCalls, completeCalls := 0, 0
	p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			if initiated && !completed {
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>key</Key><UploadId>private-upload</UploadId></Upload></ListMultipartUploadsResult>`)
			} else {
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			}
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			createCalls++
			initiated = true
			metadata = r.Header.Clone()
			if !validEncryptionHeaders(metadata, &e) || metadata.Get("X-Amz-Server-Side-Encryption-Context") != e.Selection.Context || metadata.Get("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey) != e.Proof() {
				t.Error("multipart did not capture encryption")
			}
			for name, values := range encryptionFixtureHeaders(e) {
				w.Header()[name] = values
			}
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>physical</Bucket><Key>key</Key><UploadId>private-upload</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && r.URL.Query().Get("uploadId") == "private-upload":
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != "abc" || r.Header.Get("X-Amz-Server-Side-Encryption") != "" {
				t.Error("part changed bytes or encryption policy", err)
			}
			w.Header().Set("ETag", `"part"`)
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "private-upload":
			completeCalls++
			if !completed {
				completed = true
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			} else {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			}
		case r.Method == http.MethodHead:
			for name, values := range metadata {
				if strings.HasPrefix(strings.ToLower(name), "x-amz-") {
					w.Header()[name] = values
				}
			}
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"multipart"`)
		default:
			t.Error("unexpected multipart operation", r.Method, r.URL)
		}
	}, encryptionKeyResponse())
	e = encryptionSelection(t, placement, "aws:kms:dsse")
	session := uuid.NewString()
	create := MultipartCreateRequest{SessionID: session, Key: "key", SizeBytes: 3}
	id, err := p.EnsureEncryptedMultipart(t.Context(), "physical", create, e)
	if err != nil || id != "private-upload" {
		t.Fatal("encrypted initiation", id, err)
	}
	adopted, err := p.EnsureEncryptedMultipart(t.Context(), "physical", create, e)
	if err != nil || adopted != id {
		t.Fatal("initiation recovery", adopted, err)
	}
	signed, err := p.PresignMultipartPart(t.Context(), "physical", MultipartPartRequest{Key: "key", ProviderUploadID: id, PartNumber: 1, SizeBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, signed.URL, strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range signed.Headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
		t.Fatal("part", readErr, closeErr)
	}
	complete := MultipartCompleteRequest{SessionID: session, Key: "key", ProviderUploadID: id, SizeBytes: 3, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	if _, err = p.CompleteEncryptedMultipart(t.Context(), "physical", complete, ObjectWriteConditions{}, e); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost response became success", err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var restored ResolvedObjectEncryption
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	restarted := restartEncryptionS3(t, p)
	complete.Recovering = true
	out, err := restarted.CompleteEncryptedMultipart(t.Context(), "physical", complete, ObjectWriteConditions{}, restored)
	if err != nil || out.ETag != `"multipart"` || out.Encryption.KeyID != e.Selection.KeyID {
		t.Fatal("encrypted multipart recovery", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if createCalls != 1 || completeCalls != 2 {
		t.Fatal("unexpected mutation replay", createCalls, completeCalls)
	}
}

func TestS3EncryptionAcknowledgmentCannotLie(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(http.Header)
	}{
		{"missing mode", func(h http.Header) { h.Del("X-Amz-Server-Side-Encryption") }},
		{"different mode", func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption", "AES256") }},
		{"duplicate mode", func(h http.Header) { h.Add("X-Amz-Server-Side-Encryption", "aws:kms") }},
		{"missing key", func(h http.Header) { h.Del("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") }},
		{"different key", func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "private-other-key") }},
		{"duplicate key", func(h http.Header) { h.Add("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", encryptionTestNativeKey) }},
		{"bad bucket key", func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", "perhaps") }},
		{"raw-key acknowledgment", func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Customer-Key", "private-key-material") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e ResolvedObjectEncryption
			calls := atomic.Int32{}
			p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				for name, values := range encryptionFixtureHeaders(e) {
					w.Header()[name] = values
				}
				tc.modify(w.Header())
				w.Header().Set("ETag", `"written"`)
			}, encryptionKeyResponse())
			e = encryptionSelection(t, placement, "aws:kms")
			_, err := p.WriteEncryptedObject(t.Context(), "physical", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{}, e)
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) || calls.Load() != 1 || strings.Contains(err.Error(), "private-") {
				t.Fatal("invalid encrypted acknowledgment settled or leaked", err, calls.Load())
			}
		})
	}
}

func TestS3KMSKeyValidationAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want           error
	}{
		{"valid", encryptionKeyResponse(), nil},
		{"different identity", strings.Replace(encryptionKeyResponse(), encryptionTestNativeKey, "private-different-key", 1), ErrConfiguration},
		{"wrong owning provider account", strings.Replace(encryptionKeyResponse(), `"AWSAccountId":"111122223333"`, `"AWSAccountId":"444455556666"`, 1), ErrConfiguration},
		{"disabled", strings.Replace(encryptionKeyResponse(), `"Enabled":true`, `"Enabled":false`, 1), ErrConfiguration},
		{"pending deletion", strings.Replace(encryptionKeyResponse(), `"KeyState":"Enabled"`, `"KeyState":"PendingDeletion"`, 1), ErrConfiguration},
		{"asymmetric", strings.Replace(encryptionKeyResponse(), "SYMMETRIC_DEFAULT", "RSA_2048", 1), ErrConfiguration},
		{"signing", strings.Replace(encryptionKeyResponse(), "ENCRYPT_DECRYPT", "SIGN_VERIFY", 1), ErrConfiguration},
		{"AWS managed", strings.Replace(encryptionKeyResponse(), `"KeyManager":"CUSTOMER"`, `"KeyManager":"AWS"`, 1), ErrConfiguration},
		{"duplicate metadata", `{"KeyMetadata":{},"KeyMetadata":` + strings.TrimPrefix(encryptionKeyResponse(), `{"KeyMetadata":`), ErrUnavailable},
		{"duplicate enabled", strings.Replace(encryptionKeyResponse(), `"Enabled":true`, `"Enabled":false,"Enabled":true`, 1), ErrUnavailable},
		{"trailing document", encryptionKeyResponse() + ` {}`, ErrUnavailable},
		{"oversized", strings.Repeat("x", api.MaxObjectEncryptionProviderResponseBytes+1), ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := atomic.Int32{}
			p, placement := encryptionS3Fixture(t, func(http.ResponseWriter, *http.Request) { mutations.Add(1) }, tc.response)
			e := encryptionSelection(t, placement, "aws:kms")
			err := p.CheckEncryptionKey(t.Context(), e)
			if !errors.Is(err, tc.want) || mutations.Load() != 0 || err != nil && strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe key qualification", err, mutations.Load())
			}
		})
	}
	for _, code := range []string{"AccessDeniedException", "NotFoundException", "ThrottlingException"} {
		t.Run(code, func(t *testing.T) {
			calls := atomic.Int32{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"__type":"%s","message":"private-key-detail"}`, code)
			}))
			defer server.Close()
			b := encryptionTestBackend()
			b.Encryption.KMSEndpoint, b.AllowHTTP = server.URL, true
			_, placement := encryptionTestRegistry(t, b)
			e := encryptionSelection(t, placement, "aws:kms")
			err := placement.Provider.(ObjectEncryptionProvider).CheckEncryptionKey(t.Context(), e)
			want := ErrConfiguration
			if code == "ThrottlingException" {
				want = ErrUnavailable
			}
			if !errors.Is(err, want) || calls.Load() != 1 || strings.Contains(err.Error(), "private-key-detail") {
				t.Fatal("KMS error leaked or retried", err, calls.Load())
			}
		})
	}
}

func TestS3EncryptionKeyJSONDepthBound(t *testing.T) {
	for _, depth := range []int{api.MaxObjectEncryptionJSONDepth - 1, api.MaxObjectEncryptionJSONDepth + 1} {
		body := []byte(`{"extra":` + strings.Repeat("[", depth) + `0` + strings.Repeat("]", depth) + `}`)
		if validEncryptionKeyJSON(body) != (depth < api.MaxObjectEncryptionJSONDepth) {
			t.Fatal("KMS JSON depth bound", depth)
		}
	}
}

func TestS3EncryptedMultipartCompletionVerifiesInitiationProof(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		badProof, duplicateProof bool
	}{
		{"verified", false, false}, {"changed context", true, false}, {"duplicate private proof", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e ResolvedObjectEncryption
			session := uuid.NewString()
			calls := atomic.Int32{}
			p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				for name, values := range encryptionFixtureHeaders(e) {
					w.Header()[name] = values
				}
				w.Header().Set("X-Amz-Version-Id", "immutable-result")
				switch r.Method {
				case http.MethodPost:
					_, _ = io.Copy(io.Discard, r.Body)
					_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>"completed"</ETag></CompleteMultipartUploadResult>`)
				case http.MethodHead:
					if r.URL.Query().Get("versionId") != "immutable-result" {
						t.Error("completion inspected mutable current object")
					}
					proof := e.Proof()
					if tc.badProof {
						proof = "different-initiation"
					}
					w.Header().Set("Content-Length", "3")
					w.Header().Set("ETag", `"completed"`)
					w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, session)
					w.Header().Set("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey, proof)
					if tc.duplicateProof {
						w.Header().Add("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey, "another")
					}
				default:
					t.Error("unexpected request", r.Method)
				}
			}, encryptionKeyResponse())
			e = encryptionSelection(t, placement, "aws:kms")
			out, err := p.CompleteEncryptedMultipart(t.Context(), "physical", MultipartCompleteRequest{SessionID: session, Key: "key", ProviderUploadID: "upload", SizeBytes: 3, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}, ObjectWriteConditions{}, e)
			if tc.badProof || tc.duplicateProof {
				if !errors.Is(err, ErrUnavailable) || out.ETag != "" {
					t.Fatal("unverified completion settled", out, err)
				}
			} else if err != nil || out.ETag != `"completed"` {
				t.Fatal("verified completion rejected", out, err)
			}
			if calls.Load() != 2 {
				t.Fatal("unexpected completion attempts", calls.Load())
			}
		})
	}
}

func TestS3EncryptedHistoricalProofAfterOverwrite(t *testing.T) {
	for _, badProof := range []bool{false, true} {
		t.Run(strconv.FormatBool(badProof), func(t *testing.T) {
			var e ResolvedObjectEncryption
			receipt := uuid.NewString()
			p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>retained-write</VersionId><Size>3</Size></Version></ListVersionsResult>`)
				case http.MethodHead:
					if r.URL.Query().Get("versionId") != "retained-write" {
						t.Error("history selected mutable current object")
					}
					for name, values := range encryptionFixtureHeaders(e) {
						w.Header()[name] = values
					}
					proof := e.Proof()
					if badProof {
						proof = "different-selection"
					}
					w.Header().Set("X-Amz-Version-Id", "retained-write")
					w.Header().Set("Content-Length", "3")
					w.Header().Set("ETag", `"retained"`)
					w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
					w.Header().Set("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey, proof)
				default:
					t.Error("unexpected history operation", r.Method)
				}
			}, encryptionKeyResponse())
			e = encryptionSelection(t, placement, "aws:kms")
			billed := 0
			out, err := p.ConfirmEncryptedObjectHistory(t.Context(), "physical", ObjectHistoryProofRequest{Key: "key", Receipt: receipt, SizeBytes: 3, BeforeRequest: func(context.Context) error { billed++; return nil }}, e)
			if badProof {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("history accepted foreign encryption", err)
				}
			} else if err != nil || out.ETag != `"retained"` || out.Encryption.KeyID != e.Selection.KeyID {
				t.Fatal("encrypted history proof", out, err)
			}
			if billed != 2 {
				t.Fatal("unmetered history operation", billed)
			}
		})
	}
}

func TestS3PermissionProbeCannotReplaceNativeKMSAuthorization(t *testing.T) {
	calls := atomic.Int32{}
	p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>KMS.AccessDeniedException</Code><Message>private-key-detail</Message></Error>`)
	}, encryptionKeyResponse())
	e := encryptionSelection(t, placement, "aws:kms")
	_, err := p.WriteEncryptedObject(t.Context(), "physical", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{}, e)
	if !errors.Is(err, ErrWriteRejected) || !errors.Is(err, ErrConfiguration) || calls.Load() != 1 || strings.Contains(err.Error(), "private-key-detail") {
		t.Fatal("DescribeKey replaced native permission enforcement", err, calls.Load())
	}
}

func TestS3EncryptionBucketKeyChoiceIsExplicit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			var e ResolvedObjectEncryption
			p, placement := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.Header.Get("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled") != strconv.FormatBool(enabled) {
					t.Error("bucket-key choice was inherited")
				}
				for name, values := range encryptionFixtureHeaders(e) {
					w.Header()[name] = values
				}
				w.Header().Set("ETag", `"bucket-key"`)
			}, encryptionKeyResponse())
			requested := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: placement.Encryption.Keys[0].Reference}
			if enabled {
				requested.BucketKeyEnabled = &enabled
			}
			var err error
			e, err = placement.Encryption.Resolve(encryptionTestAccount, requested)
			if err != nil {
				t.Fatal(err)
			}
			if e.Selection.BucketKeyEnabled == nil || *e.Selection.BucketKeyEnabled != enabled {
				t.Fatal("implicit mutable bucket-key policy")
			}
			out, err := p.WriteEncryptedObject(t.Context(), "physical", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{}, e)
			if err != nil || out.Encryption.BucketKeyEnabled == nil || *out.Encryption.BucketKeyEnabled != enabled {
				t.Fatal("bucket-key response", out, err)
			}
		})
	}
}

func TestS3EncryptionPreDispatchIsolation(t *testing.T) {
	var calls atomic.Int32
	p, placement := encryptionS3Fixture(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) }, encryptionKeyResponse())
	e := encryptionSelection(t, placement, "AES256")
	e.AccountID = uuid.NewString()
	// AES256 also binds to the dispatch account. A caller must compare the
	// captured account to its actor before invoking this internal adapter.
	if err := placement.Encryption.VerifySnapshot(encryptionTestAccount, e); !errors.Is(err, ErrConfiguration) {
		t.Fatal("foreign immutable account accepted", err)
	}
	kms := encryptionSelection(t, placement, "aws:kms")
	kms.AccountID = uuid.NewString()
	if _, err := p.WriteEncryptedObject(context.Background(), "physical", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{}, kms); !errors.Is(err, ErrWriteRejected) {
		t.Fatal("cross-owner dispatch accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatal("cross-owner native mutation")
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Metadata: map[string]string{strings.ToUpper(ReservedObjectEncryptionMetadataKey): "forged"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("customer forged encryption proof", err)
	}
}
