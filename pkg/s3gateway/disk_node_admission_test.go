package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func setDiskAdmissionPressure(t *testing.T, s *objectnode.Store, blocked bool) {
	t.Helper()
	o := objectnode.DefaultWriteAdmissionOptions()
	o.MinFreeBytes, o.ResumeFreeBytes = 1, 2
	if blocked {
		o.MinFreeBytes, o.ResumeFreeBytes = 1<<50, (1<<50)+1
	}
	if err := s.ConfigureWriteAdmission(o); err != nil {
		t.Fatal(err)
	}
}

func diskAdmissionRequest(t *testing.T, method, path string, before, after func(*http.Request)) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "http://node.test"+path, nil)
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	r.Header.Set(objectstorage.OwnedNodeAdmissionHeader, "put")
	r.Header.Set(objectstorage.OwnedNodeAdmissionSizeHeader, "1")
	if before != nil {
		before(r)
	}
	if err := s3Signer().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if after != nil {
		after(r)
	}
	return r
}

func TestDiskWriteAdmissionPrivateProbe(t *testing.T) {
	s, h := diskFixture(t)
	if err := s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	setDiskAdmissionPressure(t, s, false)
	for _, tc := range []struct {
		name, method, path string
		before, after      func(*http.Request)
		want               int
	}{
		{name: "ready", method: "HEAD", path: "/test-bucket", want: 200},
		{name: "SDK bucket path", method: "HEAD", path: "/test-bucket/", want: 200},
		{name: "unsigned selector", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Del(objectstorage.OwnedNodeAdmissionHeader) }, after: func(r *http.Request) { r.Header.Set(objectstorage.OwnedNodeAdmissionHeader, "put") }, want: 403},
		{name: "unsigned size", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Del(objectstorage.OwnedNodeAdmissionSizeHeader) }, after: func(r *http.Request) { r.Header.Set(objectstorage.OwnedNodeAdmissionSizeHeader, "1") }, want: 403},
		{name: "missing size", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Del(objectstorage.OwnedNodeAdmissionSizeHeader) }, want: 400},
		{name: "duplicate size", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Add(objectstorage.OwnedNodeAdmissionSizeHeader, "1") }, want: 403},
		{name: "noncanonical size", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Set(objectstorage.OwnedNodeAdmissionSizeHeader, "01") }, want: 400},
		{name: "unknown operation", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Set(objectstorage.OwnedNodeAdmissionHeader, "erase") }, want: 400},
		{name: "get", method: "GET", path: "/test-bucket", want: 400},
		{name: "write", method: "PUT", path: "/test-bucket", want: 400},
		{name: "object", method: "HEAD", path: "/test-bucket/key", want: 400},
		{name: "query", method: "HEAD", path: "/test-bucket?uploads", want: 400},
		{name: "replication query", method: "GET", path: "/?gregale-replication=status", want: 400},
		{name: "condition", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Set("If-Match", "*") }, want: 400},
		{name: "range", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Set("Range", "bytes=0-1") }, want: 400},
		{name: "crossed selector", method: "HEAD", path: "/test-bucket", before: func(r *http.Request) { r.Header.Set(objectstorage.OwnedNodeReceiptHeader, uuid.NewString()) }, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, diskAdmissionRequest(t, tc.method, tc.path, tc.before, tc.after))
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.want == 200 && (w.Header().Get(objectstorage.OwnedNodeAdmissionStatusHeader) != "ready" || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal(w.Header())
			}
		})
	}
	setDiskAdmissionPressure(t, s, true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, diskAdmissionRequest(t, "HEAD", "/test-bucket", nil, nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" || w.Header().Get(objectstorage.OwnedNodeAdmissionStatusHeader) != "blocked" {
		t.Fatal(w.Code, w.Header())
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest("GET", "http://metrics.test/metrics", nil))
	if !strings.Contains(metrics.Body.String(), "faas_object_node_write_admission_pressure_free_bytes 1") {
		t.Fatal("missing pressure metric")
	}
}

func TestDiskWriteAdmissionDoesNotCreateReceiptOrBlockExistingProof(t *testing.T) {
	s, h := diskFixture(t)
	if err := s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.Put(t.Context(), "test-bucket", "kept", strings.NewReader("x"), 1, objectnode.PutOptions{Receipt: id}); err != nil {
		t.Fatal(err)
	}
	setDiskAdmissionPressure(t, s, true)
	newID := uuid.NewString()
	r := diskSignedPut(t, "blocked", strings.NewReader("x"), 1)
	r.Header.Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, newID)
	if err := s3Signer().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "SlowDown") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := s.Receipt("test-bucket", "blocked", newID); !errors.Is(err, objectnode.ErrNoReceipt) {
		t.Fatal("blocked request gained a decision", err)
	}
	o, f, err := s.Get("test-bucket", "kept")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if string(body) != "x" || o.Size != 1 {
		t.Fatal(o, string(body))
	}
	if _, err = s.Receipt("test-bucket", "kept", id); err != nil {
		t.Fatal("pressure blocked existing proof", err)
	}
}

func TestDiskWriteAdmissionHintNeverConfirmsReplica(t *testing.T) {
	s, h, _, _, _ := replicaDiskFixture(t)
	setDiskAdmissionPressure(t, s, false)
	id := uuid.NewString()
	if _, err := s.Put(t.Context(), "test-bucket", "pending", strings.NewReader("x"), 1, objectnode.PutOptions{Receipt: id}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, diskAdmissionRequest(t, "HEAD", "/test-bucket", nil, nil))
	after, err := s.ReplicationStatus()
	if w.Code != 200 || err != nil || before.Durability.ConfirmedSequence != after.Durability.ConfirmedSequence || after.Durability.ConfirmedSequence >= after.AppliedSequence {
		t.Fatal("pressure hint changed durability", w.Code, before, after, err)
	}
}

func TestDiskWriteAdmissionMandatoryReplicaPinReopensAfterConfirmation(t *testing.T) {
	s, h, follower, _, source := replicaDiskFixture(t)
	o := objectnode.DefaultWriteAdmissionOptions()
	o.MinFreeBytes, o.ResumeFreeBytes, o.LagHigh, o.LagLow = 1, 2, 4, 3
	if err := s.ConfigureWriteAdmission(o); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(t.Context(), "test-bucket", "pending", strings.NewReader("x"), 1, objectnode.PutOptions{Receipt: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, diskAdmissionRequest(t, "HEAD", "/test-bucket", nil, nil))
	if w.Code != 503 || s.AdmissionStatus().Reasons&objectnode.AdmissionLag == 0 {
		t.Fatal("mandatory confirmation did not pin lag", w.Code, s.AdmissionStatus())
	}
	confirmDiskReplica(t, follower, source)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, diskAdmissionRequest(t, "HEAD", "/test-bucket", nil, nil))
	if w.Code != 200 {
		t.Fatal("confirmation did not reopen admission", w.Code, w.Body.String())
	}
}
