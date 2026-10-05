package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEncryptionHeaderContract(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, header, value, signed string
		invalid, unsupported                        bool
	}{
		{name: "explicit AES", method: "PUT", target: "/assets/key", header: "X-Amz-Server-Side-Encryption", value: "AES256", signed: "host;x-amz-server-side-encryption"},
		{name: "unsigned selection", method: "PUT", target: "/assets/key", header: "X-Amz-Server-Side-Encryption", value: "AES256", signed: "host", invalid: true},
		{name: "empty header", method: "PUT", target: "/assets/key", header: "X-Amz-Server-Side-Encryption", signed: "host;x-amz-server-side-encryption", invalid: true},
		{name: "read cipher directive", method: "GET", target: "/assets/key", header: "X-Amz-Server-Side-Encryption", value: "AES256", unsupported: true},
		{name: "part cipher directive", method: "PUT", target: "/assets/key?uploadId=public&partNumber=1", header: "X-Amz-Server-Side-Encryption", value: "AES256", unsupported: true},
		{name: "customer supplied secret", method: "PUT", target: "/assets/key", header: "X-Amz-Server-Side-Encryption-Customer-Key", value: "private", unsupported: true},
		{name: "unknown cipher directive", method: "PUT", target: "/assets/key", header: "X-Amz-Server-Side-Encryption-Unknown", value: "private", unsupported: true},
		{name: "query injection", method: "PUT", target: "/assets/key?x-amz-server-side-encryption=AES256", unsupported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			if got := hasUnsupportedS3Semantics(r); got != tc.unsupported {
				t.Fatal("unsupported semantics", got)
			}
			_, err := encryptionFromHeaders(r, tc.signed)
			if (err != nil) != tc.invalid {
				t.Fatal(err)
			}
		})
	}
	r := httptest.NewRequest(http.MethodPut, "/assets/key", nil)
	r.Header.Set("X-Amz-Server-Side-Encryption", "AES256")
	r.Header["x-amz-server-side-encryption"] = []string{"AES256"}
	if _, err := encryptionFromHeaders(r, "host;x-amz-server-side-encryption"); err == nil {
		t.Fatal("case alias duplicates accepted")
	}
}

func TestPublicEncryptionAmbiguousAcknowledgmentMem(t *testing.T) {
	st := state.NewMemStore()
	publicEncryptionAmbiguousAcknowledgment(t, st)
}
func TestPublicEncryptionAmbiguousAcknowledgmentPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	publicEncryptionAmbiguousAcknowledgment(t, st)
}
func publicEncryptionAmbiguousAcknowledgment(t *testing.T, st multipartCopyIntegrationStore) {
	f, native, key := publicEncryptionFixture(t, st)
	native.tamper = true
	_, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("uncertain"), Body: strings.NewReader("hello"), ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: aws.String(key)})
	if err == nil || strings.Contains(err.Error(), publicEncryptionNativeKey) || strings.Contains(err.Error(), "private-wrong-key") {
		t.Fatal("ambiguous acknowledgment accepted or leaked", err)
	}
	receipts, err := st.(state.ObjectWriteReceiptStore).ListObjectWriteReceipts(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "pending", 10, "")
	if err != nil || len(receipts.Items) != 1 {
		t.Fatal("uncertain journal lost", receipts, err)
	}
	credential, _, err := st.ResolveObjectS3Credential(t.Context(), testAccess)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.(state.ObjectTrackedGatewayUploadStore).GetObjectUploadReceipt(t.Context(), f.bucket.AccountID, f.bucket.AppID, "", credential.ID, receipts.Items[0].ID)
	if err != nil || c.WritePhase != state.ObjectUploadDispatched || c.Encryption.Selection.KeyID != key {
		t.Fatal("immutable recovery intent", c, err)
	}
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	native.tamper = false
	native.disabled = true
	native.mu.Unlock()
	proof, err := backend.Provider.(objectstorage.ObjectEncryptionProvider).ConfirmEncryptedObject(t.Context(), f.bucket.PhysicalName, c.Key, c.ID, c.Bytes, c.Encryption)
	if err != nil {
		t.Fatal("recovery depended on enabled key", err)
	}
	c.Status, c.ETag, c.ProviderVersionID, c.VerifiedEncryption = "completed", proof.ETag, proof.ProviderVersionID, proof.Encryption
	if _, err = st.(state.ObjectTrackedGatewayUploadStore).FinishTrackedObjectUpload(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.puts != 1 || native.keys != 1 {
		t.Fatal("recovery replayed write or key probe", native.puts, native.keys)
	}
}

func TestPublicEncryptionPresignedRequestAndReadPrivacy(t *testing.T) {
	f, native, key := publicEncryptionFixture(t, state.NewMemStore())
	signed, err := awss3.NewPresignClient(f.client).PresignPutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("presigned"), ContentLength: aws.Int64(5), ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: aws.String(key)})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, signed.URL, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range signed.SignedHeader {
		request.Header[name] = values
	}
	response, err := f.client.Options().HTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != key {
		t.Fatal("SDK presigned encryption", response.StatusCode, response.Header)
	}
	native.mu.Lock()
	before := native.requests
	native.mu.Unlock()
	request, err = http.NewRequestWithContext(t.Context(), http.MethodPut, signed.URL, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range signed.SignedHeader {
		request.Header[name] = values
	}
	request.Header.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", publicEncryptionNativeKey)
	response, err = f.client.Options().HTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("tampered signed key accepted", response.StatusCode)
	}
	native.mu.Lock()
	if native.requests != before {
		t.Error("tampered request reached provider")
	}
	native.tamper = true
	native.mu.Unlock()
	out, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("presigned")})
	if out != nil && out.Body != nil {
		_ = out.Body.Close()
	}
	if err == nil || strings.Contains(err.Error(), "private-wrong-key") || strings.Contains(err.Error(), publicEncryptionNativeKey) {
		t.Fatal("read leaked bytes or native identity", out, err)
	}
}

func TestPublicEncryptionDisabledCopySettlesWithoutDispatch(t *testing.T) {
	st := state.NewMemStore()
	f, native, key := publicEncryptionFixture(t, st)
	native.mu.Lock()
	native.disabled = true
	native.objects["put"] = publicEncryptionObject{headers: http.Header{}, data: "hello"}
	native.mu.Unlock()
	_, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("disabled-copy"), CopySource: aws.String("assets/put"), TaggingDirective: types.TaggingDirectiveReplace, ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: aws.String(key)})
	if err == nil {
		t.Fatal("disabled key copied")
	}
	page, err := st.ListObjectWriteReceipts(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "failed", 10, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ErrorCode != "provider_write_rejected" {
		t.Fatal("predispatch copy rejection did not settle", page, err)
	}
	pending, err := st.ListObjectWriteReceipts(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "pending", 10, "")
	if err != nil || len(pending.Items) != 0 {
		t.Fatal("copy left a pending reservation", pending, err)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.puts != 0 || native.keys != 1 || native.requests != 2 {
		t.Fatal("key validation dispatched or replayed a copy", native.requests, native.keys, native.puts)
	}
}
