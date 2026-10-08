package objectstorage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 628
// Opt-in qualification uses only pre-created, disposable, labeled fixtures.
// The caller owns IAM setup and cleanup; this test never configures production.
func TestGCSLiveFeatureQualification(t *testing.T) {
	if os.Getenv("FAAS_GCS_FEATURE_LIVE") != "1" {
		t.Skip("disposable live GCS fixture required")
	}
	bucket, other := os.Getenv("FAAS_GCS_FEATURE_BUCKET"), os.Getenv("FAAS_GCS_FEATURE_OTHER_BUCKET")
	if !strings.HasPrefix(bucket, "gregale-cli-") || !strings.HasPrefix(other, "gregale-cli-") || bucket == other || os.Getenv("FAAS_OBJECT_STORAGE_CONFIG") == "" {
		t.Fatal("unsafe or absent native qualification fixture")
	}
	registry, err := Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := backend.Provider.(*GCS)
	if !ok {
		t.Fatal("qualification requires GCS")
	}
	probe, _ := http.NewRequestWithContext(t.Context(), "HEAD", "https://storage.googleapis.com/"+bucket+"/qualification/absent", nil)
	probeResponse, probeErr := p.httpClient.Do(probe)
	if probeErr != nil {
		message := probeErr.Error()
		credentialRaw, _ := os.ReadFile(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
		var descriptor map[string]any
		_ = json.Unmarshal(credentialRaw, &descriptor)
		for _, field := range []string{"client_secret", "refresh_token"} {
			if v, ok := descriptor[field].(string); ok && v != "" {
				message = strings.ReplaceAll(message, v, "[redacted]")
			}
		}
		message = regexp.MustCompile(`https?://[^\s"<>]+`).ReplaceAllString(message, "[url]")
		t.Fatalf("native credential preflight: %s", message)
	}
	_ = probeResponse.Body.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	t.Run("versioning controls", func(t *testing.T) {
		if err := p.PutBucketVersioning(ctx, bucket, "Enabled"); err != nil {
			t.Fatal(err)
		}
		v, err := p.GetBucketVersioning(ctx, bucket)
		if err != nil || v.Status != "Enabled" {
			t.Fatal(v, err)
		}
		// GCS asks callers to wait at least 30 seconds after enabling versioning.
		select {
		case <-time.After(31 * time.Second):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})
	key, receipt := "qualification/目录 /+%.txt", uuid.NewString()
	var first UploadResult
	var second UploadResult
	t.Run("tracked put and current proof", func(t *testing.T) {
		first, err = p.WriteTrackedObject(ctx, bucket, key, receipt, strings.NewReader("payload"), 7, ObjectMetadata{ContentType: "text/plain", Metadata: map[string]string{"owner": "qualification"}, Tags: map[string]string{"team": "storage"}})
		if err != nil || first.ProviderVersionID == "" {
			t.Fatal(first, err)
		}
		proof, err := p.ConfirmTrackedObject(ctx, bucket, key, receipt, 7)
		if err != nil || proof.ProviderVersionID != first.ProviderVersionID || proof.ETag != first.ETag {
			t.Fatal(proof, err)
		}
	})
	if first.ProviderVersionID == "" {
		t.FailNow()
	}
	t.Run("cross bucket copy fences", func(t *testing.T) {
		source, err := p.SnapshotCopySource(ctx, bucket, key)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.CopyCrossBucketTrackedObject(ctx, bucket, other, uuid.NewString(), CopyObjectRequest{SourceKey: key, DestinationKey: "qualification/copy"}, source, CopySourceConditions{IfMatch: source.ETag}, ResolvedObjectEncryption{})
		if err != nil || out.ProviderVersionID == "" {
			t.Fatal(out, err)
		}
		tags, err := p.GetObjectTags(ctx, other, "qualification/copy")
		if err != nil || tags["team"] != "storage" {
			t.Fatal(tags, err)
		}
	})
	t.Run("AES256 intent and bucket default", func(t *testing.T) {
		e, err := p.encryption.Resolve(uuid.NewString(), api.ObjectEncryption{Algorithm: "AES256"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.WriteEncryptedObject(ctx, bucket, "qualification/encrypted", uuid.NewString(), strings.NewReader("encrypted"), 9, ObjectMetadata{}, e)
		if err != nil || out.Encryption.Algorithm != "AES256" || out.ProviderVersionID == "" {
			t.Fatal(out, err)
		}
		native, err := p.GetBucketEncryption(ctx, bucket)
		if err != nil || native.Algorithm != "AES256" {
			t.Fatal(native, err)
		}
		if err := p.PutBucketEncryption(ctx, bucket, e, native); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("retained receipt and version read", func(t *testing.T) {
		second, err = p.WriteTrackedObject(ctx, bucket, key, uuid.NewString(), strings.NewReader("new"), 3, ObjectMetadata{})
		if err != nil || second.ProviderVersionID == "" {
			t.Fatal(err)
		}
		page, err := p.ConfirmTrackedObjectHistory(ctx, bucket, ObjectHistoryProofRequest{Key: key, Receipt: receipt, SizeBytes: 7, BeforeRequest: func(context.Context) error { return nil }})
		if err != nil || page.UploadResult.ProviderVersionID != first.ProviderVersionID || page.UploadResult.ETag != first.ETag {
			t.Fatal(page, err)
		}
		signed, err := p.PresignVersionRead(ctx, bucket, "GET", key, first.ProviderVersionID, false, 60)
		if err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequestWithContext(ctx, "GET", signed.URL, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal("signed native version read interrupted")
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 || string(body) != "payload" || response.Header.Get("X-Goog-Generation") != first.ProviderVersionID {
			t.Fatal("incorrect native generation read", response.StatusCode)
		}
	})
	t.Run("interoperable version listing", func(t *testing.T) {
		page, err := p.ListObjectVersionPage(ctx, bucket, ObjectVersionListRequest{Prefix: key, Limit: 1})
		if err != nil || len(page.Items) != 1 || page.Items[0].Key != key || page.NextKeyMarker != key {
			t.Fatal(page, err)
		}
		next, err := p.ListObjectVersionPage(ctx, bucket, ObjectVersionListRequest{Prefix: key, KeyMarker: page.NextKeyMarker, ProviderVersionMarker: page.NextProviderVersionMarker, Limit: 100})
		if err != nil || len(next.Items) != 1 || next.Items[0].Key != key || next.NextKeyMarker != "" {
			t.Fatal(next, err)
		}
		versions := map[string]bool{page.Items[0].ProviderVersionID: page.Items[0].IsLatest, next.Items[0].ProviderVersionID: next.Items[0].IsLatest}
		oldLatest, oldFound := versions[first.ProviderVersionID]
		newLatest, newFound := versions[second.ProviderVersionID]
		if len(versions) != 2 || !oldFound || oldLatest || !newFound || !newLatest {
			t.Fatal("incorrect native version identities", versions)
		}
		prefixes, err := p.ListObjectVersionPage(ctx, bucket, ObjectVersionListRequest{Prefix: "qualification/目录", Delimiter: "/", Limit: 100})
		if err != nil || len(prefixes.CommonPrefixes) != 1 || prefixes.CommonPrefixes[0] != "qualification/目录 /" {
			t.Fatal(prefixes, err)
		}
	})
	t.Run("multipart copied range and generation receipt", func(t *testing.T) {
		n := int64(api.MinMultipartPartBytes + 1)
		if _, err := p.WriteTrackedObject(ctx, bucket, "qualification/large", uuid.NewString(), strings.NewReader(strings.Repeat("x", int(n))), n, ObjectMetadata{}); err != nil {
			t.Fatal(err)
		}
		source, err := p.SnapshotMultipartCopySource(ctx, bucket, "qualification/large")
		if err != nil {
			t.Fatal(err)
		}
		session, key := uuid.NewString(), "qualification/part-copy"
		id, err := p.EnsureMultipartUpload(ctx, other, MultipartCreateRequest{SessionID: session, Key: key, SizeBytes: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer p.AbortMultipartUpload(context.WithoutCancel(ctx), other, MultipartAbortRequest{Key: key, ProviderUploadID: id})
		reserved := int64(0)
		copyCtx := WithMultipartCopyReadRecorder(ctx, func(_ context.Context, n int64) error { reserved += n; return nil })
		part, err := p.CopyCrossBucketMultipartPart(copyCtx, bucket, other, MultipartPartCopyRequest{SourceKey: "qualification/large", Key: key, ProviderUploadID: id, PartNumber: 1, Range: &CopySourceRange{First: 1, Last: 3}}, source)
		if err != nil || reserved != 3 {
			t.Fatal(part, err, reserved)
		}
		done, err := p.CompleteMultipartWithResult(ctx, other, MultipartCompleteRequest{SessionID: session, Key: key, ProviderUploadID: id, SizeBytes: 3, Parts: []CompletedPart{{PartNumber: 1, ETag: part.ETag}}}, ObjectWriteConditions{})
		if err != nil || done.ProviderVersionID == "" {
			t.Fatal(done, err)
		}
		recovered, err := p.recoverGCSMultipart(ctx, other, MultipartCompleteRequest{SessionID: session, Key: key, SizeBytes: 3}, "")
		if err != nil || recovered.ProviderVersionID != done.ProviderVersionID || recovered.ETag != done.ETag {
			t.Fatal(recovered, err)
		}
	})
	t.Run("stale current deletion preserves replacement", func(t *testing.T) {
		generation, _ := gcsGeneration(first.ProviderVersionID)
		if err := p.deleteCapturedCurrent(ctx, bucket, key, []string{fmt.Sprintf("%064x", generation)}); err != nil {
			t.Fatal(err)
		}
		current, err := p.store.ObjectState(ctx, bucket, key)
		if err != nil || current.Size != 3 || current.Version == generation {
			t.Fatal(current, err)
		}
		if _, err := p.DeleteObjectVersion(ctx, bucket, key, first.ProviderVersionID); err != nil {
			t.Fatal(err)
		}
	})
}
