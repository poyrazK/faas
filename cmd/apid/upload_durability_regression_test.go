package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestUploadAppendStorageFailureDoesNotAcknowledgeMissingBytes(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "durable-upload"}, nil)
	session := startSession(t, e, "durable-upload", 8)
	row, err := e.store.GetUploadSession(context.Background(), session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	// Move only this test's spool aside to simulate a storage-open failure.
	backup := row.PartPath + ".saved"
	if err := os.Rename(row.PartPath, backup); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, session.UploadID, 0, []byte("abcd")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed storage append = %d: %s", rec.Code, rec.Body.String())
	}
	row, err = e.store.GetUploadSession(context.Background(), session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ReceivedBytes != 0 {
		t.Errorf("storage failure acknowledged %d missing bytes", row.ReceivedBytes)
	}
	if err := os.Rename(backup, row.PartPath); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, session.UploadID, 0, []byte("abcd")); rec.Code != http.StatusOK {
		t.Fatalf("retry after storage recovery = %d: %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(row.PartPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "abcd" {
		t.Fatalf("recovered spool = %q, want abcd prefix", data)
	}
}

func TestUploadAppendRejectsBytesPastDeclaredSize(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "bounded-upload"}, nil)
	session := startSession(t, e, "bounded-upload", 4)
	if rec := appendChunk(t, e, session.UploadID, 0, []byte("abc")); rec.Code != http.StatusOK {
		t.Fatalf("first append = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := appendChunk(t, e, session.UploadID, 3, []byte("de")); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized final append = %d: %s, want 413", rec.Code, rec.Body.String())
	}
	row, err := e.store.GetUploadSession(context.Background(), session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ReceivedBytes != 3 {
		t.Errorf("rejected append advanced offset to %d", row.ReceivedBytes)
	}
	info, err := os.Stat(row.PartPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 {
		t.Errorf("rejected append extended spool to %d bytes", info.Size())
	}
}

type uploadAppendFaultStore struct {
	state.Store
	beforeAppend func()
	fail         bool
}

func (s *uploadAppendFaultStore) AppendUploadBytes(ctx context.Context, in sqlc.AppendUploadBytesParams) (sqlc.AppendUploadBytesRow, error) {
	s.beforeAppend()
	if s.fail {
		return sqlc.AppendUploadBytesRow{}, errors.New("injected database failure")
	}
	return s.Store.AppendUploadBytes(ctx, in)
}

func TestUploadAppendPersistsBytesBeforeOffsetCAS(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "ordered-upload"}, nil)
	session := startSession(t, e, "ordered-upload", 4)
	row, err := e.store.GetUploadSession(context.Background(), session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	fault := &uploadAppendFaultStore{Store: e.store, fail: true, beforeAppend: func() {
		data, readErr := os.ReadFile(row.PartPath)
		if readErr != nil || string(data) != "abcd" {
			t.Errorf("offset CAS reached before spool write: bytes=%q, error=%v", data, readErr)
		}
	}}
	e.s.store = fault
	if rec := appendChunk(t, e, session.UploadID, 0, []byte("abcd")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("injected CAS failure = %d: %s", rec.Code, rec.Body.String())
	}
	fault.fail = false
	if rec := appendChunk(t, e, session.UploadID, 0, []byte("abcd")); rec.Code != http.StatusOK {
		t.Fatalf("retry after CAS failure = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadStartRejectedOptionsDoNotLeakSpool(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e := setup(t, api.PlanFree)
	e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "invalid-upload"}, nil)
	// This valid JSON fits the request limit, but HTML escaping when the
	// options are re-encoded exceeds the persisted-options limit.
	body := `{"app_slug":"invalid-upload","total_size":4,"deploy_options":{"reason":"` + strings.Repeat("<", 200000) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized options = %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(spool)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected upload left %d orphan spool files without a session", len(entries))
	}
}
