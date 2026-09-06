package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
)

type pageWakeBackend struct {
	*fakeBackend
	delay time.Duration
}

func (b *pageWakeBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	select {
	case <-time.After(b.delay):
	case <-ctx.Done():
		return "", WakeMethodUnspecified, false, ctx.Err()
	}
	return b.fakeBackend.Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
}

type pageWakeAudit struct {
	mu   sync.Mutex
	rows []pageWakeAuditRow
}

type pageWakeAuditRow struct {
	kind    string
	subject *string
	data    map[string]any
}

func (a *pageWakeAudit) Emit(_ context.Context, kind string, subject *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, pageWakeAuditRow{kind: kind, subject: subject, data: data})
}

func (a *pageWakeAudit) snapshot() []pageWakeAuditRow {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]pageWakeAuditRow(nil), a.rows...)
}

func TestBrowserColdWakeServesRetryPageAndRecordsTimeline(t *testing.T) {
	base, backend, _ := newTestHandler(t)
	pageBackend := &pageWakeBackend{fakeBackend: backend, delay: 1800 * time.Millisecond}
	audit := &pageWakeAudit{}
	h := NewHandlerWith(pageBackend, NewMetrics(), base.log).WithWakePageAudit(audit)

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	req.Header.Set("Accept", "text/html")
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("browser wake page took %v, want <2s", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`http-equiv="refresh"`, `fetch(`, `x-faas-wake-id`, `data-faas-page="waking-up"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wake page missing %q", want)
		}
	}
	if got := rec.Header().Get(wakePageHeader); got != "1" {
		t.Errorf("%s = %q, want 1", wakePageHeader, got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && pageBackend.HealthyCount(backend.app.ID) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if pageBackend.HealthyCount(backend.app.ID) == 0 {
		t.Fatal("detached wake did not make the app routable")
	}

	retry := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	retry.Header.Set("Accept", "text/html")
	retryRec := httptest.NewRecorder()
	h.ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusOK || retryRec.Body.String() != "hello from app" {
		t.Fatalf("retry response = %d %q, want 200 app body", retryRec.Code, retryRec.Body.String())
	}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(audit.snapshot()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	rows := audit.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want one page_served row", len(rows))
	}
	row := rows[0]
	if row.kind != events.WakePageServed {
		t.Fatalf("audit kind = %q, want %q", row.kind, events.WakePageServed)
	}
	if got := row.data["wake_id"]; got != "fake-wake-id" {
		t.Errorf("wake_id = %v, want fake-wake-id", got)
	}
	if got := row.data["app_id"]; got != backend.app.ID {
		t.Errorf("app_id = %v, want %s", got, backend.app.ID)
	}
	if row.subject == nil || *row.subject != backend.app.AccountID {
		t.Errorf("subject = %v, want %s", row.subject, backend.app.AccountID)
	}
	if _, ok := row.data["served_at"]; !ok {
		t.Error("page_served row missing served_at")
	}
}
