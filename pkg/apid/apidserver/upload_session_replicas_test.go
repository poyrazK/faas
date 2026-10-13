// adr: 1237
// upload_session_replicas_test.go pins the multi-replica upload stopgap: with
// FAAS_APID_RESUMABLE_UPLOADS=0 (set on cells with more than one control
// node) POST /v1/uploads answers 404 without allocating spool, which makes
// the CLI fall back to the single-request upload.
package apidserver_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1237
func TestUploadSession_OffOnMultiReplicaCell(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)
	t.Setenv("FAAS_APID_RESUMABLE_UPLOADS", "0")
	e := apidserver.Setup(t, api.PlanFree)

	rec := e.Do(t, "POST", "/v1/uploads", apidserver.StartUploadRequest{
		AppSlug: "any", TotalSize: 1024,
	}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 so the CLI falls back to the single-request upload, got %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spool root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("disabled resumable uploads allocated spool entries: %+v", entries)
	}
}
