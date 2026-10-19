// adr: 1027 — the resize handler hides storage growth outside its preview:
// storage_limit_bytes is an unknown field and views carry no limits.
package managedpg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeResizes struct {
	got managedpostgres.ResizeDatabaseRequest
	op  managedpostgres.ResizeOperation
}

func (f *fakeResizes) Resize(_ context.Context, r managedpostgres.ResizeDatabaseRequest) (managedpostgres.ResizeOperation, error) {
	f.got = r
	f.op.TargetClass, f.op.TargetStorageLimitBytes = r.TargetClass, r.TargetStorageLimitBytes
	return f.op, nil
}

func (f *fakeResizes) GetResize(context.Context, string, string, string) (managedpostgres.ResizeOperation, error) {
	return managedpostgres.ResizeOperation{}, managedpostgres.ErrNotFound
}

func TestResizesHideStorageOutsideThePreview(t *testing.T) {
	const id = "55555555-5555-4555-8555-555555555555"
	service := &fakeResizes{op: managedpostgres.ResizeOperation{ID: id, DatabaseID: "orders", Generation: 2, State: managedpostgres.ResizePending,
		SourceSpec: managedpostgres.Spec{Class: managedpostgres.ClassDevelopment, StorageLimitBytes: 1 << 30}, CreatedAt: time.Unix(0, 0)}}
	nobody, _ := ParseRollout("TEST", "")
	everyone, _ := ParseRollout("TEST", "*")
	var problems []error
	serve := func(storage Rollout, body string) *httptest.ResponseRecorder {
		rs := Resizes{Service: service, Storage: func() Rollout { return storage },
			Problem:       func(w http.ResponseWriter, err error) { problems = append(problems, err); w.WriteHeader(http.StatusBadRequest) },
			NotConfigured: func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }}
		r := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/orders/resize", strings.NewReader(body))
		r.SetPathValue("id", "orders")
		w := httptest.NewRecorder()
		rs.Post(w, r, state.Account{ID: "acct-1", Plan: api.PlanPro})
		return w
	}
	for _, body := range []string{
		`{"request_id":"` + id + `","storage_limit_bytes":2147483648}`,
		`{"request_id":"` + id + `","service_class":"burstable","storage_limit_bytes":0}`,
	} {
		if w := serve(nobody, body); w.Code != http.StatusBadRequest || !errors.Is(problems[len(problems)-1], managedpostgres.ErrInvalid) {
			t.Errorf("%s outside the preview = %d", body, w.Code)
		}
	}
	w := serve(nobody, `{"request_id":"`+id+`","service_class":"burstable"}`)
	if w.Code != http.StatusAccepted || strings.Contains(w.Body.String(), "storage") {
		t.Fatalf("class resize outside the preview = %d %s", w.Code, w.Body.String())
	}
	w = serve(everyone, `{"request_id":"`+id+`","storage_limit_bytes":2147483648}`)
	var view api.ManagedPostgresResize
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.TargetStorageLimitBytes != 2<<30 || view.FromStorageLimitBytes != 1<<30 ||
		service.got.TargetStorageLimitBytes != 2<<30 {
		t.Fatalf("storage growth in the preview = %d %s", w.Code, w.Body.String())
	}
}
