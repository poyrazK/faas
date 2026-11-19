package storage

// adr: 20261117190000 — slice 4. Runs against a real S3-compatible bucket
// like s3_live_test.go: skipped unless FAAS_S3_LIVE_BUCKET and the other
// FAAS_S3_LIVE_* variables are set. Use a disposable bucket.

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// signerGranter grants everything with a signer, as secretd would for a
// placed tenant.
type signerGranter struct {
	signer storagegrant.Signer
	asked  []storagegrant.Op
}

func (g *signerGranter) GrantObjects(ctx context.Context, reqs []storagegrant.Request) ([]storagegrant.Grant, error) {
	out := make([]storagegrant.Grant, len(reqs))
	for i, r := range reqs {
		g.asked = append(g.asked, r.Op)
		grant, err := g.signer.Sign(ctx, r, time.Now().Add(storagegrant.TTL))
		if err != nil {
			return nil, err
		}
		out[i] = grant
	}
	return out, nil
}

func liveGrantedS3(t *testing.T) (*S3StorageBackend, *signerGranter, *s3.Client, string) {
	t.Helper()
	bucket := os.Getenv("FAAS_S3_LIVE_BUCKET")
	if bucket == "" {
		t.Skip("FAAS_S3_LIVE_BUCKET not set")
	}
	client := s3.New(s3.Options{
		Region:                     os.Getenv("FAAS_S3_LIVE_REGION"),
		BaseEndpoint:               aws.String(strings.TrimSuffix(os.Getenv("FAAS_S3_LIVE_ENDPOINT"), "/")),
		UsePathStyle:               os.Getenv("FAAS_S3_LIVE_PATH_STYLE") == "1",
		Credentials:                credentials.NewStaticCredentialsProvider(os.Getenv("FAAS_S3_LIVE_ACCESS_KEY_ID"), os.Getenv("FAAS_S3_LIVE_SECRET_ACCESS_KEY"), ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	signer, err := storagegrant.NewS3Signer(client, bucket)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newS3StorageBackend(bucket, snapshotCompressionNone, &awsS3ArtifactStore{client: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeferDeletes(b, &recordingDeferrer{}); err != nil {
		t.Fatal(err)
	}
	g := &signerGranter{signer: signer}
	if n, err := UseGrants(b, g, http.DefaultClient); err != nil || n != 1 {
		t.Fatalf("UseGrants = %d, %v", n, err)
	}
	return b, g, client, bucket
}

func liveKey() string { return "layers/" + uuid.NewString() + ".ext4" }

func readGranted(t *testing.T, b *S3StorageBackend, key string) []byte {
	t.Helper()
	rc, err := b.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Small objects: create, then replace exactly the ETag held; reads, size
// and a missing key go through grants too.
func TestLiveGrantedS3SinglePut(t *testing.T) {
	b, g, _, _ := liveGrantedS3(t)
	key := liveKey()
	if ok, err := b.Exists(t.Context(), key); err != nil || ok {
		t.Fatalf("exists before put = %v, %v", ok, err)
	}
	if _, err := b.Get(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	for _, body := range []string{"first layer", "second, longer layer"} {
		if err := b.Put(t.Context(), key, strings.NewReader(body)); err != nil {
			t.Fatalf("put %q: %v", body, err)
		}
		if got := readGranted(t, b, key); string(got) != body {
			t.Fatalf("read %q, want %q", got, body)
		}
	}
	if ok, err := b.Exists(t.Context(), key); err != nil || !ok {
		t.Fatalf("exists after put = %v, %v", ok, err)
	}
	var create, replace bool
	for _, op := range g.asked {
		create = create || op == storagegrant.OpCreate
		replace = replace || op == storagegrant.OpReplace
	}
	if !create || !replace {
		t.Fatalf("ops asked %v; want a create then a replace", g.asked)
	}
	if err := b.Delete(t.Context(), key); err != nil {
		t.Fatalf("deferred delete: %v", err)
	}
	if ok, _ := b.Exists(t.Context(), key); !ok {
		t.Fatal("a granted node deleted")
	}
}

// Objects over one part go up as multipart uploads, each request a grant.
func TestLiveGrantedS3Multipart(t *testing.T) {
	b, g, _, _ := liveGrantedS3(t)
	key := liveKey()
	for round := 0; round < 2; round++ {
		body := make([]byte, s3PartSize*2+12345)
		if _, err := rand.Read(body); err != nil {
			t.Fatal(err)
		}
		if err := b.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
			t.Fatalf("multipart put round %d: %v", round, err)
		}
		if got := readGranted(t, b, key); !bytes.Equal(got, body) {
			t.Fatalf("round %d read %d bytes, want %d equal bytes", round, len(got), len(body))
		}
	}
	counts := map[storagegrant.Op]int{}
	for _, op := range g.asked {
		counts[op]++
	}
	if counts[storagegrant.OpCreateMultipart] != 2 || counts[storagegrant.OpUploadPart] != 6 || counts[storagegrant.OpCompleteMultipart] != 2 {
		t.Fatalf("ops asked %v", counts)
	}
}

// The signed conditions hold: a create on a held key and a replace of a
// stale ETag are refused, and dropping a signed header breaks the grant.
func TestLiveGrantedS3ConditionsAreSigned(t *testing.T) {
	b, g, _, _ := liveGrantedS3(t)
	key := liveKey()
	if err := b.Put(t.Context(), key, strings.NewReader("held")); err != nil {
		t.Fatal(err)
	}
	send := func(r storagegrant.Request, drop string) int {
		t.Helper()
		gr, err := g.signer.Sign(t.Context(), r, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(t.Context(), gr.Method, gr.URL, strings.NewReader("intruder"))
		for k, v := range gr.Headers {
			if k != drop {
				req.Header.Set(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := send(storagegrant.Request{Key: key, Op: storagegrant.OpCreate}, ""); code != http.StatusPreconditionFailed {
		t.Errorf("create on a held key = HTTP %d, want 412", code)
	}
	if code := send(storagegrant.Request{Key: key, Op: storagegrant.OpReplace, ETag: `"0123456789abcdef0123456789abcdef"`}, ""); code != http.StatusPreconditionFailed {
		t.Errorf("replace of a stale ETag = HTTP %d, want 412", code)
	}
	// AWS answers 403, MinIO 400: either way the write is refused.
	unconditioned := liveKey()
	if code := send(storagegrant.Request{Key: unconditioned, Op: storagegrant.OpCreate}, "if-none-match"); code != http.StatusForbidden && code != http.StatusBadRequest {
		t.Errorf("create without its signed condition = HTTP %d, want a refusal", code)
	}
	if ok, err := b.Exists(t.Context(), unconditioned); err != nil || ok {
		t.Errorf("a create without its signed condition wrote: %v, %v", ok, err)
	}
	if got := readGranted(t, b, key); string(got) != "held" {
		t.Fatalf("held object changed to %q", got)
	}
}
