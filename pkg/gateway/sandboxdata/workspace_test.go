// adr: 20261027090013 step 7 — hydrate unpacks an upload within the plan's
// workspace cap, and persist answers the node's recorded snapshot, keeping
// the limit or guest refusal it raised.

package sandboxdata_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func (f *fakeVmmd) PersistSandboxWorkspace(_ context.Context, req *vmmdpb.PersistSandboxWorkspaceRequest) (*vmmdpb.PersistSandboxWorkspaceResponse, error) {
	f.mu.Lock()
	f.persists = append(f.persists, req)
	err := f.persistErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &vmmdpb.PersistSandboxWorkspaceResponse{SnapshotId: "snap-1", Bytes: 512, Sha256: "ab", Entries: 3,
		ExpiresAtUnixMs: testNow.Add(72 * 3600e9).UnixMilli()}, nil
}

func TestHydrateUnpacksWithinTheWorkspaceCap(t *testing.T) {
	f := newFixture(t)
	c := placed()
	c.WorkspaceMaxBytes = 8
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:hydrate?path=src", f.token(t, c), strings.NewReader("tar.gz"))
	if rec.Code != http.StatusOK {
		t.Fatalf("hydrate = %d %s", rec.Code, rec.Body)
	}
	var got api.HydrateSandboxWorkspaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Bytes != 6 || got.Entries != 2 {
		t.Fatalf("hydrate answer %+v, %v", got, err)
	}
	req := f.vmmd.requests()[0]
	if req.Op != sandboxproto.OpArchiveExtract || req.Path != "src" || req.MaxBytes != 8 || req.MaxEntries != api.SandboxArchiveEntriesMax {
		t.Fatalf("guest request %+v", req)
	}
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:hydrate", f.token(t, c), strings.NewReader("a larger archive"))
	if rec.Code != http.StatusRequestEntityTooLarge || problemCode(t, rec) != api.CodeSandboxTooLarge {
		t.Fatalf("over the cap = %d %s", rec.Code, rec.Body)
	}
	// A token from before tokens carried the cap is read again.
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:hydrate", f.token(t, placed()), strings.NewReader("x"))
	if rec.Code != http.StatusConflict || problemCode(t, rec) != api.CodeSandboxNotReady {
		t.Fatalf("uncapped token = %d %s", rec.Code, rec.Body)
	}
}

func TestPersistAnswersTheRecordedSnapshot(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:persist", f.token(t, placed()), strings.NewReader(`{"path":"out"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("persist = %d %s", rec.Code, rec.Body)
	}
	var got api.PersistSandboxWorkspaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.SnapshotID != "snap-1" || got.Bytes != 512 || got.Entries != 3 ||
		!got.ExpiresAt.Equal(testNow.Add(72*3600e9)) {
		t.Fatalf("persist answer %+v, %v", got, err)
	}
	if p := f.vmmd.persists[0]; p.GetInstance() != "vm-1" || p.GetSandboxId() != "sbx-1" || p.GetPath() != "out" {
		t.Fatalf("vmmd request %+v", p)
	}
	if rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:persist", f.token(t, placed()), nil); rec.Code != http.StatusCreated {
		t.Fatalf("persist without a body = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:persist", f.token(t, placed()), strings.NewReader(`{"path":"../x"}`)); rec.Code != http.StatusForbidden || len(f.vmmd.persists) != 2 {
		t.Fatalf("persist outside the workspace = %d %s", rec.Code, rec.Body)
	}
}

func TestPersistKeepsTheRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"quota", grpcerr.ToStatus(api.ErrAgentLimit(api.PlanPro, "SandboxSnapshotBytesPerAccount", 10, 12)), http.StatusForbidden, api.CodeAgentLimit},
		{"guest", grpcerr.ToStatus(sandboxproto.Problem(sandboxproto.ErrorFrame{Code: sandboxproto.CodeTooLarge})), http.StatusRequestEntityTooLarge, api.CodeSandboxTooLarge},
		{"not recorded", grpcerr.ToStatus(api.NewProblem(503, api.CodeSandboxWorkspaceUnavailable, "x", "y")), http.StatusServiceUnavailable, api.CodeSandboxWorkspaceUnavailable},
		{"session gone", status.Error(codes.NotFound, "gone"), http.StatusNotFound, api.CodeSandboxNotFound},
		{"plain failure", status.Error(codes.Unavailable, "x"), http.StatusServiceUnavailable, api.CodeInternal},
	} {
		f := newFixture(t)
		f.vmmd.persistErr = tc.err
		rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/workspace:persist", f.token(t, placed()), nil)
		if rec.Code != tc.status || problemCode(t, rec) != tc.code {
			t.Errorf("%s: persist = %d %s, want %d %s", tc.name, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
}
