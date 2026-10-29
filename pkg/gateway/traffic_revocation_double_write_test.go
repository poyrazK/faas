package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// A request already refused before enrollment got two problem documents in
// one body: the guard wrote the refusal, then its body wrote it again.
func TestEnrollTrafficScopesWritesOneRefusal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://app.example.test/", nil)
	ctx, cancel, _ := reqbudget.WithStarted(req.Context(), time.Now().Add(-time.Second), time.Millisecond, api.RequestBudgetMax, "forward", "stream")
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	if !enrollTrafficScopesWith(rec, req, &trafficrevocation.Registry{}, nil) {
		t.Fatal("expired request was enrolled")
	}
	dec := json.NewDecoder(rec.Body)
	var first map[string]any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("refusal body: %v", err)
	}
	var second map[string]any
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		t.Fatalf("a second problem document followed the first: %v %v", second, err)
	}
}
