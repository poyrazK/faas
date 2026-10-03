package s3gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type transferHTTPFixture struct {
	path            string
	mu              sync.Mutex
	size            int64
	receipt, digest string
	puts            atomic.Int32
	accepted        chan struct{}
	release         <-chan struct{}
}

func (p *transferHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method == http.MethodPut {
		p.puts.Add(1)
		f, err := os.Create(p.path)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, hash), r.Body)
		closeErr := f.Close()
		if err != nil || closeErr != nil || n != r.ContentLength {
			t.Error(n, r.ContentLength, err, closeErr)
			w.WriteHeader(500)
			return
		}
		p.mu.Lock()
		p.size, p.receipt, p.digest = n, r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey), hex.EncodeToString(hash.Sum(nil))
		p.mu.Unlock()
		if p.accepted != nil {
			close(p.accepted)
		}
		if p.release != nil {
			select {
			case <-p.release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("ETag", `"local-transfer"`)
		w.WriteHeader(200)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		t.Error("unexpected provider request", r.Method, r.URL)
		w.WriteHeader(500)
		return
	}
	p.mu.Lock()
	size, receipt := p.size, p.receipt
	p.mu.Unlock()
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"local-transfer"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, receipt)
	if r.Method == http.MethodGet {
		f, err := os.Open(p.path) //nolint:forbidigo // Only opens the fixture's own file in its private test directory.
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer f.Close()
		if _, err = io.Copy(w, f); err != nil {
			t.Error(err)
		}
	}
}

func transferIntegrationConfig(timeout int64) objectstorage.Config {
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 512 << 20, MaxBucketBytes: 512 << 20, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 512 << 20, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	return objectstorage.Config{Accounting: &policy, MaxUploadBytes: 512 << 20, MaxSinglePutBytes: 96 << 20, MaxPartBytes: 96 << 20, Transfer: objectstorage.ObjectTransferConfig{Profile: "direct", TimeoutSeconds: timeout, MaxConcurrentUploads: 2, MaxSpoolBytes: 96 << 20, MinSpoolFreeBytes: 1}}
}

// adr: 411
func TestProductionTransferEndToEndMem(t *testing.T) {
	productionTransferEndToEnd(t, state.NewMemStore())
}
func TestProductionTransferEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	productionTransferEndToEnd(t, st)
}

func productionTransferEndToEnd(t *testing.T, st multipartCopyIntegrationStore) {
	const size = int64(65 << 20)
	release := make(chan struct{})
	p := &transferHTTPFixture{path: filepath.Join(t.TempDir(), "object"), accepted: make(chan struct{}), release: release}
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), transferIntegrationConfig(3600), 0)
	options := f.client.Options()
	httpClient := *options.HTTPClient.(*http.Client)
	transport := httpClient.Transport.(*http.Transport).Clone()
	transport.ExpectContinueTimeout = 5 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/second.bin") && r.Header.Get("Expect") != "100-continue" {
			t.Error("large admission probe lost Expect header")
		}
		return transport.RoundTrip(r)
	})
	options.HTTPClient = &httpClient
	f.client = awss3.New(options)
	source, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Truncate(size); err != nil {
		t.Fatal(err)
	}
	complete := make(chan error, 1)
	go func() {
		_, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("large.bin"), Body: io.NewSectionReader(source, 0, size), ContentLength: aws.Int64(size)})
		complete <- err
	}()
	select {
	case <-p.accepted:
	case <-time.After(20 * time.Second):
		close(release)
		t.Fatal("large PUT never reached provider")
	}
	// Both slots are available to policy, but two 65 MiB files exceed the
	// configured 96 MiB aggregate spool. Rejection must precede dispatch.
	_, err = f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("second.bin"), Body: io.NewSectionReader(source, 0, size), ContentLength: aws.Int64(size)}, func(o *awss3.Options) {
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Build.Add(middleware.BuildMiddlewareFunc("TransferExpectContinue", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
				in.Request.(*smithyhttp.Request).Header.Set("Expect", "100-continue")
				// Smithy's request starts with zero protocol fields. Go only
				// waits for 100-continue with an explicit HTTP/1.1 request.
				in.Request.(*smithyhttp.Request).ProtoMajor = 1
				in.Request.(*smithyhttp.Request).ProtoMinor = 1
				return next.HandleBuild(ctx, in)
			}), middleware.After)
		})
	})
	var service smithy.APIError
	if !errors.As(err, &service) || service.ErrorCode() != "SlowDown" {
		close(release)
		t.Fatal("aggregate spool did not stop the second request", err)
	}
	close(release)
	if err = <-complete; err != nil {
		t.Fatal(err)
	}
	out, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("large.bin")})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, out.Body)
	closeErr := out.Body.Close()
	if err != nil || closeErr != nil || n != size {
		t.Fatal(n, err, closeErr)
	}
	p.mu.Lock()
	digest, receipt := p.digest, p.receipt
	p.mu.Unlock()
	if hex.EncodeToString(hash.Sum(nil)) != digest || p.puts.Load() != 1 {
		t.Fatal("bytes changed or provider write was replayed")
	}
	expected := sha256.New()
	if _, err = io.Copy(expected, io.NewSectionReader(source, 0, size)); err != nil || hex.EncodeToString(expected.Sum(nil)) != digest {
		t.Fatal("source bytes changed", err)
	}
	journal := st.(state.ObjectWriteReceiptStore)
	r, err := journal.GetObjectWriteReceipt(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, receipt)
	if err != nil || r.Status != "completed" || r.Bytes != size {
		t.Fatal(r, err)
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).CapacityBytes != size {
		t.Fatal("large upload accounting", usage, err)
	}
	f.handler.spoolMu.Lock()
	reserved := f.handler.spoolReserved
	f.handler.spoolMu.Unlock()
	if reserved != 0 || len(f.handler.putSlots) != 0 {
		t.Fatal("large upload leaked spool reservation")
	}
}

func TestProductionTransferTimeoutKeepsDispatchedReceiptMem(t *testing.T) {
	transferTimeoutReceipt(t, state.NewMemStore())
}
func TestProductionTransferTimeoutKeepsDispatchedReceiptPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	receipt, bucket := transferTimeoutReceipt(t, st)
	restarted := state.NewPgStore(pool)
	r, err := restarted.GetObjectWriteReceipt(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID, receipt)
	if err != nil || r.Status != "pending" || r.Bytes != 1 {
		t.Fatal("restart lost uncertain transfer", r, err)
	}
}

func transferTimeoutReceipt(t *testing.T, st multipartCopyIntegrationStore) (string, state.ObjectBucket) {
	p := &transferHTTPFixture{path: filepath.Join(t.TempDir(), "object"), release: make(chan struct{})}
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), transferIntegrationConfig(1), 0)
	_, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("uncertain.bin"), Body: strings.NewReader("x")})
	if err == nil || p.puts.Load() != 1 {
		t.Fatal("timeout did not retain exactly one dispatched attempt", err)
	}
	p.mu.Lock()
	receipt := p.receipt
	p.mu.Unlock()
	r, err := st.(state.ObjectWriteReceiptStore).GetObjectWriteReceipt(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, receipt)
	if err != nil || r.Status != "pending" {
		t.Fatal("timeout settled the uncertain write", r, err)
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).CapacityBytes != 1 {
		t.Fatal("timeout refunded the dispatched write", usage, err)
	}
	return receipt, f.bucket
}
