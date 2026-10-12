// adr: 93

package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// slowChunk delivers its bytes only after delay, as a slow uplink does.
type slowChunk struct {
	data  []byte
	delay time.Duration
	sent  bool
}

func (b *slowChunk) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	b.sent = true
	return copy(p, b.data), nil
}

// TestUploadChunkBookkeepingOutlivesASlowBody pins H9-157 for the chunk
// route: the request budget ran while the chunk arrived, so on production-us
// a chunk that took longer than the budget failed afterwards at "reload
// upload session before appending" or "advance upload offset" with
// context deadline exceeded. 16 MiB at the supported 512 KiB/s is 32 s.
func TestUploadChunkBookkeepingOutlivesASlowBody(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.SetupPGHandler(t, api.PlanFree)
	if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "slow-up"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.Do(t, "POST", "/v1/uploads", apidserver.StartUploadRequest{AppSlug: "slow-up", TotalSize: 4}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start upload: %d %s", rec.Code, rec.Body.String())
	}
	var sess apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}

	// The request's deadline passes while the body is still arriving.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPatch, "/v1/uploads/"+sess.UploadID, &slowChunk{data: []byte{1, 2, 3, 4}, delay: 600 * time.Millisecond}).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	out := httptest.NewRecorder()
	(*e.H()).ServeHTTP(out, req)
	if out.Code != http.StatusOK || out.Header().Get("Upload-Offset") != strconv.Itoa(4) {
		t.Fatalf("slow chunk = %d offset %q %s, want 200 at offset 4", out.Code, out.Header().Get("Upload-Offset"), out.Body.String())
	}
}
