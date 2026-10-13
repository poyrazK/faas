package s3gateway

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1038
func TestMultipartIntegrityBadDigestBeforeDispatch(t *testing.T) {
	native := &integrityNativeFixture{}
	f, in := checksumUpload(t, state.NewMemStore(), "CRC32C", objectchecksum.FullObject, native)
	// The AWS client signs the checksum assertion with the request.
	in.ChecksumCRC32C = aws.String("AAAAAA==")
	_, err := f.client.CompleteMultipartUpload(t.Context(), in)
	assertSDKErrorCode(t, err, "BadDigest")
	if native.completions != 0 {
		t.Fatal("invalid complete checksum reached provider")
	}
	in.ChecksumCRC32C = nil
	if _, err = f.client.CompleteMultipartUpload(t.Context(), in); err != nil {
		t.Fatal("invalid checksum consumed the upload", err)
	}
	in.ChecksumCRC32C = aws.String("AAAAAA==")
	_, err = f.client.CompleteMultipartUpload(t.Context(), in)
	assertSDKErrorCode(t, err, "BadDigest")
	if native.completions != 1 {
		t.Fatal("changed replay repeated the write")
	}
}

func TestMultipartIntegrityLostAckMem(t *testing.T) {
	st := state.NewMemStore()
	native := &integrityNativeFixture{loseAck: true}
	f, in := checksumUpload(t, st, "CRC32C", objectchecksum.FullObject, native)
	_, err := f.client.CompleteMultipartUpload(t.Context(), in)
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	u, err := st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, aws.ToString(in.UploadId))
	if err != nil || u.State != state.ObjectMultipartCompleting || !u.Checksum.Complete() {
		t.Fatal("uncertain checksum intent lost", u, err)
	}
	st.SetClockForTest(func() time.Time { return time.Now().Add(time.Minute) })
	// Rebuild the gateway using its durable registry/store instead of retaining
	// a transient hash or the initiating request.
	restarted, err := New(Config{Registry: f.handler.registry, Store: st, RequestMetrics: st, Host: f.handler.host, Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, HTTPClient: f.handler.client, OpenSecret: func([]byte) (string, error) { return testSecret, nil }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(restarted)
	restarted.host = server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	client := awss3.New(f.client.Options(), func(o *awss3.Options) { o.BaseEndpoint = aws.String(server.URL) })
	out, err := client.CompleteMultipartUpload(t.Context(), in)
	if err != nil || out == nil || native.completions != 1 {
		t.Fatal("lost completion checksum not recovered", out, err, native.completions)
	}
	u, err = st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, aws.ToString(in.UploadId))
	if err != nil || u.State != state.ObjectMultipartCompleted || !u.Checksum.Complete() {
		t.Fatal(u, err)
	}
}

func TestMultipartIntegrityLostAckRestartPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	native := &integrityNativeFixture{loseAck: true}
	f, in := checksumUpload(t, st, "SHA256", objectchecksum.Composite, native)
	_, err := f.client.CompleteMultipartUpload(t.Context(), in)
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	id := aws.ToString(in.UploadId)
	if _, err = pool.Exec(t.Context(), `UPDATE object_storage_multipart_uploads SET retry_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	reopened := state.NewPgStore(pool)
	f.handler.store = reopened
	f.handler.multipartStore = reopened
	f.handler.requestMetrics = reopened
	out, err := f.client.CompleteMultipartUpload(t.Context(), in)
	if err != nil || out == nil || native.completions != 1 {
		t.Fatal("restarted owner lost checksum proof", out, err, native.completions)
	}
	u, err := reopened.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, id)
	if err != nil || !u.Checksum.Complete() || u.State != state.ObjectMultipartCompleted {
		t.Fatal(u, err)
	}
}

func TestMultipartIntegrityMalformedAcknowledgmentRetainsFence(t *testing.T) {
	st := state.NewMemStore()
	native := &integrityNativeFixture{malformedAck: true}
	f, in := checksumUpload(t, st, "CRC64NVME", objectchecksum.FullObject, native)
	_, err := f.client.CompleteMultipartUpload(t.Context(), in)
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	u, err := st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, aws.ToString(in.UploadId))
	if err != nil || u.State != state.ObjectMultipartCompleting || !u.CompletionDispatched {
		t.Fatal("bad acknowledgment settled or forgot mutation", u, err)
	}
	if _, err = st.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: "anything"}); err == nil {
		t.Fatal("missing checksum proof admitted")
	}
}
