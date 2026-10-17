package apidserver_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// expiringAppLookupStore expires the request context as the commit starts
// processing the archive, the way scanning a large source spent the whole
// request budget on production-us.
type expiringAppLookupStore struct {
	MemStore *state.MemStore
	expire   context.CancelFunc
}

func (s expiringAppLookupStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	s.expire()
	if err := ctx.Err(); err != nil {
		return state.App{}, err
	}
	return s.MemStore.AppBySlug(ctx, slug)
}

// H10-26: a 240 MiB commit spent the 30 s request ceiling on its scans, and
// every store call after them failed with "context deadline exceeded", so no
// retry could create the deployment. Once every byte has arrived, the commit
// finishes under its own size-scaled bound.
func TestUploadCommitFinishesAfterTheRequestBudget(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "commit-budget"}, nil)
	raw := apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("console.log(1)\n")})
	body, _ := json.Marshal(apidserver.StartUploadRequest{AppSlug: "commit-budget", TotalSize: int64(len(raw))})
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var started apidserver.StartUploadResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &started) != nil {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := appendChunk(t, e, started.UploadID, 0, raw); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body)
	}

	ctx, expire := context.WithCancel(context.Background())
	defer expire()
	(*(*e.S()).Store()) = statetest.NewMem(expiringAppLookupStore{MemStore: (*e.Store()), expire: expire})
	req = httptest.NewRequest("POST", "/v1/uploads/"+started.UploadID+"/commit", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit after the request budget: %d %s", rec.Code, rec.Body)
	}
	outcome, err := (*e.Store()).GetUploadCommitOutcome(context.Background(), started.UploadID)
	if err != nil || outcome.DeploymentID == "" {
		t.Fatalf("commit outcome = %+v, %v; want the created deployment", outcome, err)
	}
}
