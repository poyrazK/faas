package apidserver_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The sha256_hex declared at start used to be stored and never compared
// (deploy review 2026-11-10). Commit now refuses bytes that do not match,
// cancelling the session so the CLI restarts with a fresh upload.
func TestUploadSession_CommitVerifiesDeclaredSHA256(t *testing.T) {
	raw := apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{
		"index.js": []byte("console.log(1)\n"),
	})
	sum := sha256.Sum256(raw)
	good := hex.EncodeToString(sum[:])
	bad := strings.Repeat("0", 64)

	for _, tc := range []struct {
		name, sha string
		want      int
		code      string
	}{
		{name: "matching", sha: good, want: http.StatusCreated},
		{name: "matching upper case", sha: strings.ToUpper(good), want: http.StatusCreated},
		{name: "not declared", sha: "", want: http.StatusCreated},
		{name: "mismatch", sha: bad, want: http.StatusGone, code: api.CodeUploadChecksumMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanFree)
			e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "checksum"}, nil)
			started := startUpload(t, e, apidserver.StartUploadRequest{AppSlug: "checksum", TotalSize: int64(len(raw)), Sha256Hex: tc.sha}, http.StatusCreated)
			if rec := appendChunk(t, e, started.UploadID, 0, raw); rec.Code != http.StatusOK {
				t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
			}
			rec := commitUpload(e, started.UploadID)
			if rec.Code != tc.want {
				t.Fatalf("commit = %d %s, want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if tc.code == "" {
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != tc.code {
				t.Fatalf("commit problem = %s, want code %s", rec.Body.String(), tc.code)
			}
			if rec := cancelSession(t, e, started.UploadID); rec.Code != http.StatusConflict {
				t.Fatalf("mismatched session still open: cancel = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUploadSession_StartRefusesMalformedSHA256(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "checksum"}, nil)
	for _, sha := range []string{"abc", strings.Repeat("g", 64), strings.Repeat("a", 65)} {
		startUpload(t, e, apidserver.StartUploadRequest{AppSlug: "checksum", TotalSize: 10, Sha256Hex: sha}, http.StatusBadRequest)
	}
}

// Lock entries used to stay in a sync.Map for the life of apid; they are now
// dropped when the last holder or waiter unlocks.
func TestUploadSessionLock_PrunedAfterLastUnlock(t *testing.T) {
	const key = "upload-lock-prune-test"
	unlock := apidserver.LockUploadSession(key)
	acquired := make(chan func())
	go func() { acquired <- apidserver.LockUploadSession(key) }()
	select {
	case <-acquired:
		t.Fatal("second holder acquired the lock while the first held it")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if !apidserver.UploadSessionLockTracked(key) {
		t.Fatal("entry dropped while a waiter still needed it")
	}
	second := <-acquired
	second()
	if apidserver.UploadSessionLockTracked(key) {
		t.Fatal("entry kept after the last unlock")
	}
}

func startUpload(t *testing.T, e apidserver.TestEnv, in apidserver.StartUploadRequest, want int) apidserver.StartUploadResponse {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("start = %d %s, want %d", rec.Code, rec.Body.String(), want)
	}
	var resp apidserver.StartUploadResponse
	if want == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return resp
}

func commitUpload(e apidserver.TestEnv, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/uploads/"+id+"/commit", nil)
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	return rec
}
