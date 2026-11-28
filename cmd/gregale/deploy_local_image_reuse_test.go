package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
)

// adr: 20261127120000

type reuseUpload struct {
	opts api.UploadDeployOptions
	body bytes.Buffer
}

// reuseStub serves an image base and records every upload session; with
// refuseThin it answers a thin commit with that problem.
type reuseStub struct {
	base       []byte
	refuseThin *api.Problem
	uploads    []*reuseUpload
	commits    int
}

func newReuseStub(t *testing.T, base []byte, refuseThin *api.Problem) *reuseStub {
	t.Helper()
	got := &reuseStub{base: base, refuseThin: refuseThin}
	stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
		cur := func() *reuseUpload { return got.uploads[len(got.uploads)-1] }
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/image-base" && r.Method == http.MethodGet:
			summary, err := ociarchive.Inspect(bytes.NewReader(got.base))
			if err != nil {
				t.Error(err)
			}
			out := api.ImageBase{ManifestDigest: summary.ManifestDigest}
			for _, l := range summary.LayerDescriptors {
				out.Layers = append(out.Layers, api.ImageBaseLayer{Digest: l.Digest, Size: l.Size})
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
			var req api.UploadStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			got.uploads = append(got.uploads, &reuseUpload{opts: *req.DeployOptions})
			id := fmt.Sprintf("img-%d", len(got.uploads))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: id, ChunkSize: 8 * 1024 * 1024, TotalSize: req.TotalSize})
		case r.Method == http.MethodPatch:
			raw, _ := io.ReadAll(r.Body)
			cur().body.Write(raw)
			w.Header().Set("Upload-Offset", strconv.Itoa(cur().body.Len()))
		case r.Method == http.MethodPost && filepath.Base(r.URL.Path) == "commit":
			got.commits++
			if cur().opts.ReuseImageBase && got.refuseThin != nil {
				api.WriteProblem(w, got.refuseThin)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_API", (*stub.Srv()).URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return got
}

// deployArchive deploys archive as the app's image with --image-archive.
func deployArchive(t *testing.T, archive []byte) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.tar")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	return main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--image-archive", path})
}

// rebuilt is what apid makes of upload with base.
func rebuilt(t *testing.T, upload, base []byte) []byte {
	t.Helper()
	idx, err := ociarchive.IndexBase(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := ociarchive.Rebuild(bytes.NewReader(upload), idx, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestDeployLocalImageReusesBaseLayers(t *testing.T) {
	localImageRepo(t)
	base, _ := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v1")
	next, digest := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v2")
	stub := newReuseStub(t, base, nil)
	if code := deployArchive(t, next); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(stub.uploads) != 1 || stub.commits != 1 {
		t.Fatalf("uploads=%d commits=%d, want one thin upload", len(stub.uploads), stub.commits)
	}
	up := stub.uploads[0]
	if !up.opts.ReuseImageBase || up.body.Len() >= len(next) {
		t.Fatalf("reuse=%v uploaded %d bytes of %d", up.opts.ReuseImageBase, up.body.Len(), len(next))
	}
	full := rebuilt(t, up.body.Bytes(), base)
	if s, err := ociarchive.Inspect(bytes.NewReader(full)); err != nil || s.ManifestDigest != digest {
		t.Fatalf("the thin upload does not rebuild the image: %v", err)
	}
}

// A thin commit apid refuses before a deployment exists falls back to the
// full upload: the base changed meanwhile, or an apid without layer reuse
// (N-1 beside N) took it for an invalid archive.
func TestDeployLocalImageFallsBackWhenThinRefused(t *testing.T) {
	for name, refusal := range map[string]*api.Problem{
		"base changed":       api.ErrImageBaseChanged(),
		"apid without reuse": api.ErrImageArchiveInvalid("missing layer blob"),
	} {
		t.Run(name, func(t *testing.T) {
			localImageRepo(t)
			base, _ := ociarchivetest.CanonicalLayers(t, "os", "app v1")
			next, digest := ociarchivetest.CanonicalLayers(t, "os", "app v2")
			stub := newReuseStub(t, base, refusal)
			if code := deployArchive(t, next); code != 0 {
				t.Fatalf("code=%d", code)
			}
			if len(stub.uploads) != 2 || !stub.uploads[0].opts.ReuseImageBase || stub.uploads[1].opts.ReuseImageBase {
				t.Fatalf("uploads=%d, want a thin upload then a full one", len(stub.uploads))
			}
			if s, err := ociarchive.Inspect(bytes.NewReader(stub.uploads[1].body.Bytes())); err != nil || s.ManifestDigest != digest {
				t.Fatalf("the fallback upload is not the full image: %v", err)
			}
		})
	}
}

func TestDeployLocalImageNoSharedLayersUploadsInFull(t *testing.T) {
	localImageRepo(t)
	base, _ := ociarchivetest.CanonicalLayers(t, "something else")
	next, _ := ociarchivetest.CanonicalLayers(t, "os", "app")
	stub := newReuseStub(t, base, nil)
	if code := deployArchive(t, next); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(stub.uploads) != 1 || stub.uploads[0].opts.ReuseImageBase {
		t.Fatalf("uploads=%d reuse=%v, want one full upload", len(stub.uploads), stub.uploads[0].opts.ReuseImageBase)
	}
}
