package main_test

import (
	"bytes"
	"encoding/json"
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

// localImageStub accepts one resumable image upload and records what it got.
type localImageStub struct {
	opts    *api.UploadDeployOptions
	body    bytes.Buffer
	commits int
	deploys int
}

func newLocalImageStub(t *testing.T) *localImageStub {
	t.Helper()
	got := &localImageStub{}
	stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
			var req api.UploadStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			got.opts = req.DeployOptions
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "img-upload", ChunkSize: 8 * 1024 * 1024, TotalSize: req.TotalSize})
		case r.URL.Path == "/v1/uploads/img-upload" && r.Method == http.MethodPatch:
			raw, _ := io.ReadAll(r.Body)
			got.body.Write(raw)
			w.Header().Set("Upload-Offset", strconv.Itoa(got.body.Len()))
		case r.URL.Path == "/v1/uploads/img-upload/commit":
			got.commits++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		case r.URL.Path == "/v1/apps/demo/deployments":
			got.deploys++
			http.Error(w, "local images must not use the registry path", http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_API", (*stub.Srv()).URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return got
}

func localImageRepo(t *testing.T) {
	t.Helper()
	repo := main.InitZeroConfigRepo(t)
	main.MustGit(t, repo, "remote", "remove", "origin")
	main.WithCwd(t, repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main.WithResetJSONOutput(t, false)
}

// checkUploaded asserts the upload is a valid canonical archive of the
// expected image, sent through the resumable session.
func (s *localImageStub) checkUploaded(t *testing.T, wantName string) {
	t.Helper()
	if s.opts == nil || s.opts.SourceFormat != api.UploadSourceFormatOCIImageArchive || s.opts.ImageName != wantName {
		t.Fatalf("deploy options = %+v, want an image archive named %q", s.opts, wantName)
	}
	if s.commits != 1 || s.deploys != 0 {
		t.Fatalf("commits=%d registry deploys=%d", s.commits, s.deploys)
	}
	sum, err := ociarchive.Inspect(bytes.NewReader(s.body.Bytes()))
	if err != nil {
		t.Fatalf("uploaded bytes are not a canonical archive: %v", err)
	}
	if sum.ExposedPort != 8080 {
		t.Fatalf("exposed port = %d", sum.ExposedPort)
	}
}

func TestDeployImageArchiveUploadsCanonicalArchive(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	saved := filepath.Join(t.TempDir(), "app.tar")
	if err := os.WriteFile(saved, ociarchivetest.DockerSave(t, "acme/web:dev", "amd64", 8080), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--image-archive", saved}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	stub.checkUploaded(t, "acme/web:dev")
}

func TestDeployLocalImageRunsDockerSave(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	dir := t.TempDir()
	saved := filepath.Join(dir, "fixture.tar")
	if err := os.WriteFile(saved, ociarchivetest.DockerSave(t, "acme/web:dev", "amd64", 8080), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fake Docker client: `image save -o DEST NAME` copies the fixture.
	fake := filepath.Join(dir, "fake-docker")
	script := "#!/bin/sh\n[ \"$1 $2 $3\" = \"image save -o\" ] && [ \"$5\" = acme/web:dev ] || exit 3\ncp " + saved + " \"$4\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_DOCKER", fake)
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	stub.checkUploaded(t, "acme/web:dev")
}

// An archive with no tag and a file name with no stem still deploys the
// image, never the working directory's source.
func TestDeployImageArchiveWithoutNameStillUploadsImage(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	saved := filepath.Join(t.TempDir(), ".tar")
	if err := os.WriteFile(saved, ociarchivetest.DockerSave(t, "", "amd64", 8080), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--image-archive", saved}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if stub.opts == nil || stub.opts.SourceFormat != api.UploadSourceFormatOCIImageArchive || stub.commits != 1 {
		t.Fatalf("deploy options = %+v commits=%d, want the image uploaded", stub.opts, stub.commits)
	}
}

// Docker runs only after the deploy's own validation, so a refused deploy
// never writes the image to $TMPDIR.
func TestDeployLocalImageSavesAfterValidation(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	fake := filepath.Join(dir, "fake-docker")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ntouch "+marker+"\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_DOCKER", fake)
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--local-image", "acme/web", "--require-authn", "--no-require-authn"}); code == 0 {
		t.Fatal("deploy succeeded")
	}
	if _, err := os.Stat(marker); err == nil || stub.opts != nil {
		t.Fatal("docker save ran (or the upload started) before validation refused the deploy")
	}
}

func TestDeployLocalImageRefusesBeforeNetwork(t *testing.T) {
	localImageRepo(t)
	saved := filepath.Join(t.TempDir(), "app.tar")
	if err := os.WriteFile(saved, ociarchivetest.DockerSave(t, "acme/web:dev", "arm64", 0), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"both sources":    {"--local-image", "a", "--image-archive", saved},
		"registry image":  {"--image-archive", saved, "--image", "r.example/x@sha256:" + string(bytes.Repeat([]byte("a"), 64))},
		"function":        {"--image-archive", saved, "--function"},
		"preview":         {"--image-archive", saved, "--dry-run"},
		"arm64 image":     {"--image-archive", saved},
		"missing archive": {"--image-archive", filepath.Join(t.TempDir(), "none.tar")},
		"option as name":  {"--local-image=-q"},
	} {
		t.Run(name, func(t *testing.T) {
			stub := newLocalImageStub(t)
			if code := main.CmdDeployTarball(append([]string{"--name", "demo", "--app", "--no-wait"}, args...)); code == 0 {
				t.Fatal("deploy succeeded")
			}
			if stub.opts != nil || stub.commits != 0 {
				t.Fatalf("refused deploy still uploaded: %+v", stub.opts)
			}
		})
	}
}
