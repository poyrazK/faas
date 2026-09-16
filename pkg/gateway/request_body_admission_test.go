package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type uploadBudgetMatcher struct {
	noOpEdgeRuleMatcher
	rule *EdgeRuleBudgetResolved
}

func (m uploadBudgetMatcher) MatchBudget(context.Context, string, string, string) *EdgeRuleBudgetResolved {
	return m.rule
}

type delayedUploadBody struct {
	data  *bytes.Reader
	delay time.Duration
	once  sync.Once
}

func (b *delayedUploadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { time.Sleep(b.delay) })
	return b.data.Read(p)
}

func (*delayedUploadBody) Close() error { return nil }

type delayedAdmitBackend struct {
	*fakeBackend
	delay time.Duration
}

func (b *delayedAdmitBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	time.Sleep(b.delay)
	return b.fakeBackend.Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
}

func TestAdmitRequestBodyKeepsSmallBodyInMemory(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://example.test/invoke", bytes.NewBufferString("hello"))

	if handled := admitRequestBodyWithin(w, r, 16, time.Second); handled {
		t.Fatalf("admitRequestBodyWithin handled request: status=%d body=%s", w.Code, w.Body.String())
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" || r.ContentLength != 5 {
		t.Fatalf("admitted body=%q content-length=%d", got, r.ContentLength)
	}
	if _, ok := r.Body.(*admittedFileBody); ok {
		t.Fatal("small body unexpectedly spilled to disk")
	}
}

func TestAdmitRequestBodySpillsLargeBodyAndRemovesFile(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), int(requestBodyMemoryThreshold)+1)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://example.test/invoke", bytes.NewReader(payload))

	if handled := admitRequestBodyWithin(w, r, int64(len(payload))+1, time.Second); handled {
		t.Fatalf("admitRequestBodyWithin handled request: status=%d body=%s", w.Code, w.Body.String())
	}
	body, ok := r.Body.(*admittedFileBody)
	if !ok {
		t.Fatalf("body type = %T, want *admittedFileBody", r.Body)
	}
	path := body.path
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("spooled body differs from input")
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool file remains after Close: %v", err)
	}
}

func TestAdmitRequestBodyReturnsStableTooLargeProblem(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://example.test/invoke", bytes.NewBufferString("12345"))

	if handled := admitRequestBodyWithin(w, r, 4, time.Second); !handled {
		t.Fatal("oversize body was admitted")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

type blockingUploadBody struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockingUploadBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, errors.New("closed")
}

func (b *blockingUploadBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestAdmitRequestBodyReturnsCorrelatedUploadTimeout(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://example.test/invoke", nil)
	r.Body = &blockingUploadBody{closed: make(chan struct{})}
	r.ContentLength = -1

	if handled := admitRequestBodyWithin(w, r, 1024, 10*time.Millisecond); !handled {
		t.Fatal("timed out upload was admitted")
	}
	if w.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want 408", w.Code)
	}
	var p api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != api.CodeRequestUploadTimeout {
		t.Fatalf("code = %q, want %q", p.Code, api.CodeRequestUploadTimeout)
	}
}

func TestHandlerStartsExecutionBudgetAfterUploadAdmission(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	h.WithEdgeRules(uploadBudgetMatcher{rule: &EdgeRuleBudgetResolved{
		ID: "budget-1", AccountID: backend.app.AccountID, AppID: backend.app.ID,
		BudgetMs: 75,
	}}, nil, nil)

	payload := []byte("hello after a deliberately slow upload")
	req := httptest.NewRequest(http.MethodPost, "http://"+backend.host+"/invoke", nil)
	req.Body = &delayedUploadBody{data: bytes.NewReader(payload), delay: 150 * time.Millisecond}
	req.ContentLength = int64(len(payload))
	rec := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(rec, req)

	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Fatalf("request completed in %s; delayed upload did not run", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after upload longer than 75ms execution budget; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStartsExecutionBudgetAfterColdWake(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	base := &fakeBackend{
		app:  App{ID: "app-cold", AccountID: "acct-cold", Plan: api.PlanPro, Type: AppTypeFunction},
		host: "cold.example.test", upstream: upstream.Listener.Addr().String(),
	}
	backend := &delayedAdmitBackend{fakeBackend: base, delay: 150 * time.Millisecond}
	h := NewHandlerWith(backend, NewMetrics(), nil)
	h.WithEdgeRules(uploadBudgetMatcher{rule: &EdgeRuleBudgetResolved{
		ID: "budget-cold", AccountID: base.app.AccountID, AppID: base.app.ID,
		BudgetMs: 75,
	}}, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "http://"+base.host+"/invoke", nil)
	rec := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(rec, req)

	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Fatalf("request completed in %s; delayed cold wake did not run", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after wake longer than 75ms execution budget; body=%s", rec.Code, rec.Body.String())
	}
}
