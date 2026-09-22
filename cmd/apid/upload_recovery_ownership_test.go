package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type missingUploadSessionStore struct{ state.Store }

func (s missingUploadSessionStore) GetUploadSession(context.Context, string) (sqlc.UploadSession, error) {
	return sqlc.UploadSession{}, state.ErrNotFound
}

// spec: §11 — a retained commit outcome must not bypass account ownership
// when its original upload-session row is no longer available.
func TestUploadCommitRecoveryChecksOutcomeOwnership(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	id := stageRegressionUpload(t, e, "recovery-upload", map[string][]byte{"index.js": []byte("console.log('ok')\n")})
	if rec := e.do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil); rec.Code != http.StatusCreated {
		t.Fatalf("initial commit: %d %s", rec.Code, rec.Body.String())
	}
	dep, err := e.store.LatestDeployment(t.Context(), findAppID(t, e, "recovery-upload"))
	if err != nil {
		t.Fatal(err)
	}
	e.s.store = missingUploadSessionStore{Store: e.store}
	owner := e.do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil)
	assertProblem(t, owner, http.StatusConflict, api.CodeUploadSessionAlreadyCommitted)
	if !strings.Contains(owner.Body.String(), dep.ID) {
		t.Fatal("owner could not recover the committed deployment")
	}
	foreign, err := e.store.CreateAccount(t.Context(), "foreign-upload@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), foreign.ID, hash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, map[string]string{"Authorization": "Bearer " + key})
	assertProblem(t, rec, http.StatusNotFound, api.CodeUploadSessionNotFound)
	if strings.Contains(rec.Body.String(), dep.ID) {
		t.Fatal("foreign account learned the committed deployment ID")
	}
	if _, err := e.s.store.GetUploadSession(t.Context(), id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("test did not exercise the missing-session recovery path")
	}
}
