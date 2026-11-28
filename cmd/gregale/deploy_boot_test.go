package main_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
)

// bootDeployDocker is a fake Docker that boots the image and saves a real
// archive fixture, for `deploy --local-image --boot`.
func bootDeployDocker(t *testing.T) fakeBootDocker {
	t.Helper()
	docker := newFakeBootDocker(t)
	main.SetDoctorBootMemorySources(t.Cleanup, signedOut, func(context.Context, string) (int, string, error) {
		return 0, "", errors.New("app not found")
	})
	docker.write(t, "image", strings.Replace(localInspectJSON("amd64"), "3000/tcp", "8080/tcp", 1))
	if err := os.WriteFile(filepath.Join(docker.dir, "saved"), ociarchivetest.DockerSave(t, "acme/web:dev", "amd64", 8080), 0o600); err != nil {
		t.Fatal(err)
	}
	return docker
}

func dockerCallIndexOf(calls []string, prefix string) int {
	for i, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return i
		}
	}
	return -1
}

func TestDeployLocalImageBootPasses(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	docker := bootDeployDocker(t)
	serveBoot(t, docker, func(http.ResponseWriter, *http.Request) {})
	envFile := filepath.Join(t.TempDir(), "boot.env")
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--boot-env-file", envFile}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	stub.checkUploaded(t, "acme/web:dev")
	calls := docker.calls(t)
	run, save := dockerCallIndexOf(calls, "run "), dockerCallIndexOf(calls, "image save ")
	if run < 0 || save < 0 || run > save {
		t.Fatalf("docker calls %q: want the boot before the save", calls)
	}
	if !strings.Contains(calls[run], "--env PORT=8080") || !strings.Contains(calls[run], "--env-file "+envFile) || !strings.HasSuffix(calls[run], " acme/web:dev") {
		t.Fatalf("docker run %q", calls[run])
	}
}

func TestDeployLocalImageBootFailureStopsBeforeSave(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	docker := bootDeployDocker(t)
	docker.write(t, "state", "false 1 false")
	if code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot"}); code == 0 {
		t.Fatal("deploy succeeded although the image crashed")
	}
	if dockerCallIndexOf(docker.calls(t), "image save ") >= 0 || stub.opts != nil {
		t.Fatalf("image saved or uploaded after a failed boot: calls %q", docker.calls(t))
	}
	if !docker.removed(t) {
		t.Error("boot container not removed")
	}
}

func TestDeployBootFlagValidation(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	for _, args := range [][]string{
		{"--name", "demo", "--app", "--no-wait", "--boot"},
		{"--name", "demo", "--app", "--no-wait", "--image-archive", "image.tar", "--boot"},
		{"--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot-env-file", "boot.env"},
	} {
		if code := main.CmdDeployTarball(args); code == 0 {
			t.Errorf("%q: deploy accepted", args)
		}
	}
	if stub.opts != nil {
		t.Fatal("an invalid --boot combination uploaded")
	}
}
