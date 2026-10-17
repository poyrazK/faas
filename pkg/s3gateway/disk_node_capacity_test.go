package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func diskSignedPut(t *testing.T, key string, body io.Reader, size int64) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "http://node.test/test-bucket/"+key, body)
	r.ContentLength = size
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	err := s3Signer().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDiskNodeUploadAdmission(t *testing.T) {
	store, _ := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1", MaxObjectBytes: 4, MaxConcurrentWrites: 1, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	started, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release)
	held := &diskHeldReader{Reader: strings.NewReader("data"), started: started, resume: resume}
	first := httptest.NewRecorder()
	request := diskSignedPut(t, "first", held, 4)
	go func() { h.ServeHTTP(first, request); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first upload did not begin streaming")
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, diskSignedPut(t, "second", strings.NewReader("data"), 4))
	if second.Code != 503 {
		t.Fatal("exhausted upload slot was accepted", second.Code)
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first upload did not complete")
	}
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	oversized := httptest.NewRecorder()
	h.ServeHTTP(oversized, diskSignedPut(t, "second", strings.NewReader("large"), 5))
	if oversized.Code != 413 {
		t.Fatal("oversized upload was accepted", oversized.Code)
	}
	if _, _, err = store.Get("test-bucket", "second"); !errors.Is(err, objectnode.ErrNoObject) {
		t.Fatal("rejected upload was published", err)
	}
}

type diskHeldReader struct {
	*strings.Reader
	once            sync.Once
	started, resume chan struct{}
}

func (r *diskHeldReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.resume })
	return r.Reader.Read(p)
}
