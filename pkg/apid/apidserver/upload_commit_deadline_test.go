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
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// expiringOutcomeStore expires the request context the moment the commit
// records its outcome, after Enqueue has already created the deployment.
type expiringOutcomeStore struct {
	MemStore *state.MemStore
	expire   context.CancelFunc
}

func (s expiringOutcomeStore) RecordUploadCommitOutcome(ctx context.Context, arg sqlc.RecordUploadCommitOutcomeParams) (sqlc.UploadCommitOutcome, error) {
	s.expire()
	if err := ctx.Err(); err != nil {
		return sqlc.UploadCommitOutcome{}, err
	}
	return s.MemStore.RecordUploadCommitOutcome(ctx, arg)
}

// production-us hunt #9 (H9-127): the request deadline expired after Enqueue
// created the deployment, the outcome write failed, and the CLI's retry
// released a second deployment from the same upload.
func TestUploadCommitRecordsTheOutcomeAfterTheRequestDeadline(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "commit-deadline"}, nil)
	raw := apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("console.log(1)\n")})
	body, _ := json.Marshal(apidserver.StartUploadRequest{AppSlug: "commit-deadline", TotalSize: int64(len(raw))})
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
	(*(*e.S()).Store()) = statetest.NewMem(expiringOutcomeStore{MemStore: (*e.Store()), expire: expire})
	req = httptest.NewRequest("POST", "/v1/uploads/"+started.UploadID+"/commit", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit after deadline: %d %s", rec.Code, rec.Body)
	}
	outcome, err := (*e.Store()).GetUploadCommitOutcome(context.Background(), started.UploadID)
	if err != nil || outcome.DeploymentID == "" {
		t.Fatalf("commit outcome = %+v, %v; a retry would create a second deployment", outcome, err)
	}
}
