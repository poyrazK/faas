package main_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// bootDeployDocker is a fake Docker that boots the image and saves a real
// archive fixture, for `deploy --local-image --boot`. The app does not exist
// yet, as on a first deploy.
func bootDeployDocker(t *testing.T) fakeBootDocker {
	t.Helper()
	docker := newFakeBootDocker(t)
	main.SetDoctorBootMemorySources(t.Cleanup, signedOut, func(context.Context, string) (int, string, error) {
		return 0, "", &api.APIError{Problem: *api.NewProblem(http.StatusNotFound, "app_not_found", "Not found", "no such app")}
	})
	docker.write(t, "image", strings.Replace(localInspectJSON("amd64"), "3000/tcp", "8080/tcp", 1))
	if err := os.WriteFile(filepath.Join(docker.dir, "saved"), ociarchivetest.DockerSave(t, "acme/web:dev", "amd64", 8080), 0o600); err != nil {
		t.Fatal(err)
	}
	return docker
}

// deployCapturingStderr runs deploy and returns its exit code and stderr.
func deployCapturingStderr(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	return main.CmdDeployTarball(args), stderr.String()
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
	code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--boot-env-file", envFile)
	if code != 0 {
		t.Fatalf("code=%d: %s", code, stderr)
	}
	if strings.Contains(stderr, "Could not read app") {
		t.Errorf("a first deploy warned that its new app could not be read: %s", stderr)
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

func TestDeployLocalImageBootUsesSize(t *testing.T) {
	localImageRepo(t)
	newLocalImageStub(t)
	docker := bootDeployDocker(t)
	serveBoot(t, docker, func(http.ResponseWriter, *http.Request) {})
	if code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--size", "large"); code != 0 {
		t.Fatalf("code=%d: %s", code, stderr)
	}
	spec, _ := api.ResourceProfileSpecFor("large")
	calls := docker.calls(t)
	if run := dockerCallIndexOf(calls, "run "); run < 0 || !strings.Contains(calls[run], " --memory "+strconv.Itoa(spec.MemoryMB)+"m ") {
		t.Fatalf("docker calls %q: want the boot capped at --size large's %d MB", calls, spec.MemoryMB)
	}
}

func TestDeployLocalImageBootFailureStopsBeforeSave(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	docker := bootDeployDocker(t)
	docker.write(t, "state", "false 1 false")
	code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot")
	if code == 0 {
		t.Fatal("deploy succeeded although the image crashed")
	}
	if dockerCallIndexOf(docker.calls(t), "image save ") >= 0 || stub.opts != nil {
		t.Fatalf("image saved or uploaded after a failed boot: calls %q", docker.calls(t))
	}
	if !docker.removed(t) {
		t.Error("boot container not removed")
	}
	// The fix names deploy's flag, not doctor's --env-file.
	if !strings.Contains(stderr, "--boot-env-file") || strings.Contains(stderr, "with --env-file") {
		t.Errorf("fix should name --boot-env-file: %s", stderr)
	}
}

func TestDeployLocalImageBootChecksWorkersWithoutHTTP(t *testing.T) {
	for _, mode := range []string{"worker", "job"} {
		t.Run(mode, func(t *testing.T) {
			localImageRepo(t)
			stub := newLocalImageStub(t)
			docker := bootDeployDocker(t) // running, and no HTTP answer
			code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--execution-mode", mode)
			if code != 0 {
				t.Fatalf("code=%d: %s", code, stderr)
			}
			stub.checkUploaded(t, "acme/web:dev")
			if run := dockerCallIndexOf(docker.calls(t), "run "); run < 0 || strings.Contains(docker.calls(t)[run], "--publish") {
				t.Fatalf("docker calls %q", docker.calls(t))
			}
		})
	}
	t.Run("crashing worker", func(t *testing.T) {
		localImageRepo(t)
		stub := newLocalImageStub(t)
		docker := bootDeployDocker(t)
		docker.write(t, "state", "false 1 false")
		code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--execution-mode", "worker")
		if code == 0 || stub.opts != nil || !strings.Contains(stderr, "A worker must keep running") {
			t.Fatalf("code=%d uploaded=%v stderr=%s", code, stub.opts != nil, stderr)
		}
	})
}

func TestDeployLocalImageBootGRPCHealth(t *testing.T) {
	t.Run("serving", func(t *testing.T) {
		localImageRepo(t)
		stub := newLocalImageStub(t)
		docker := bootDeployDocker(t)
		serveGRPC(t, docker, true, func(hs *health.Server) { hs.SetServingStatus("shop.Orders", healthpb.HealthCheckResponse_SERVING) })
		code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--healthcheck-grpc", "--healthcheck-grpc-service", "shop.Orders")
		if code != 0 {
			t.Fatalf("code=%d: %s", code, stderr)
		}
		stub.checkUploaded(t, "acme/web:dev")
	})
	t.Run("no health service", func(t *testing.T) {
		localImageRepo(t)
		stub := newLocalImageStub(t)
		docker := bootDeployDocker(t)
		serveGRPC(t, docker, false, nil)
		code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--healthcheck-grpc")
		if code == 0 || stub.opts != nil || !strings.Contains(stderr, "does not implement grpc.health.v1.Health") {
			t.Fatalf("code=%d uploaded=%v stderr=%s", code, stub.opts != nil, stderr)
		}
	})
}

func TestDeployBootCheckInterruptedFails(t *testing.T) {
	docker := bootDeployDocker(t)
	main.SetDoctorBootTimings(t.Cleanup, 5*time.Second, 5*time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The probe's answer cancels, so the stay-up wait sees the interrupt.
	serveBoot(t, docker, func(http.ResponseWriter, *http.Request) { cancel() })
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	if code := main.RunDeployBootCheck(ctx, "acme/web:dev"); code == 0 {
		t.Fatalf("an interrupted boot check let the deploy go on: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "interrupted") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestDeployBootFlagValidation(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--boot"}, "--boot requires --local-image"},
		{[]string{"--image-archive", "image.tar", "--boot"}, "--boot requires --local-image"},
		{[]string{"--local-image", "acme/web:dev", "--boot-env-file", "boot.env"}, "--boot-env-file requires --boot"},
		{[]string{"--local-image", "acme/web:dev", "--boot=false", "--boot-env-file", "boot.env"}, "--boot-env-file requires --boot"},
		{[]string{"--boot-env-file", "boot.env"}, "--boot-env-file requires --boot"},
		{[]string{"--local-image", "acme/web:dev", "--boot", "--create-only"}, "--create-only"},
	} {
		code, stderr := deployCapturingStderr(t, append([]string{"--name", "demo", "--app", "--no-wait"}, tc.args...)...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%q: code=%d stderr=%q, want %q", tc.args, code, stderr, tc.want)
		}
	}
	if stub.opts != nil {
		t.Fatal("an invalid --boot combination uploaded")
	}
}

func TestDeployLocalImageBootReadsWorkerFromManifest(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	docker := bootDeployDocker(t) // running, and no HTTP answer
	if err := os.WriteFile("gregale.yaml", []byte("worker:\n  drain_timeout: 10s\n  stop_signal: SIGTERM\n  scale:\n    min: 1\n    max: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot")
	if code != 0 {
		t.Fatalf("code=%d: %s", code, stderr)
	}
	stub.checkUploaded(t, "acme/web:dev")
	if run := dockerCallIndexOf(docker.calls(t), "run "); run < 0 || strings.Contains(docker.calls(t)[run], "--publish") {
		t.Fatalf("gregale.yaml's worker was booted as an HTTP app: %q", docker.calls(t))
	}
	if !strings.Contains(stderr, "runs for real on this machine") {
		t.Errorf("no warning that the worker does real work: %s", stderr)
	}
}

func TestDeployLocalImageBootUsesExistingAppClass(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	docker := bootDeployDocker(t)
	main.SetDoctorBootAppClass(t.Cleanup, "worker")
	code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot")
	if code != 0 {
		t.Fatalf("code=%d: %s", code, stderr)
	}
	stub.checkUploaded(t, "acme/web:dev")
	if run := dockerCallIndexOf(docker.calls(t), "run "); run < 0 || strings.Contains(docker.calls(t)[run], "--publish") {
		t.Fatalf("the worker app was booted as an HTTP app: %q", docker.calls(t))
	}
}

func TestDeployLocalImageBootRefusesHealthPathOnWorker(t *testing.T) {
	localImageRepo(t)
	stub := newLocalImageStub(t)
	bootDeployDocker(t)
	code, stderr := deployCapturingStderr(t, "--name", "demo", "--app", "--no-wait", "--local-image", "acme/web:dev", "--boot", "--execution-mode", "worker", "--healthcheck-path", "/healthz")
	if code == 0 || stub.opts != nil || !strings.Contains(stderr, "--healthcheck-path probes HTTP") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}
